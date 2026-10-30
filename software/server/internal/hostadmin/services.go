package hostadmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
)

var (
	ErrUnsupported = errors.New("systemd is unsupported")
	ErrUnavailable = errors.New("systemd is unavailable")
	ErrInvalid     = errors.New("invalid host query")
	ErrMissing     = errors.New("service not found")
	unitPattern    = regexp.MustCompile(`^[a-zA-Z0-9_.@:-]{1,200}\.service$`)
)

const MaxServicePage = 50
const MaxServiceInventory = 5000

type Service struct {
	Name             string               `json:"name"`
	Description      string               `json:"description,omitempty"`
	Loaded           string               `json:"loaded_state"`
	Active           string               `json:"active_state"`
	Sub              string               `json:"sub_state"`
	Enabled          string               `json:"enabled_state"`
	MainPID          uint32               `json:"main_pid,omitempty"`
	Failure          string               `json:"failure,omitempty"`
	Dependencies     []string             `json:"dependencies,omitempty"`
	CanReload        bool                 `json:"can_reload"`
	Canonical        string               `json:"canonical_unit"`
	Aliases          []string             `json:"aliases,omitempty"`
	Category         ServiceCategory      `json:"management_category"`
	Management       string               `json:"management_state"`
	Protection       string               `json:"protection_reason,omitempty"`
	UnitFile         string               `json:"unit_file,omitempty"`
	UnitTrusted      bool                 `json:"unit_file_trusted"`
	MetadataComplete bool                 `json:"metadata_complete"`
	Relationships    ServiceRelationships `json:"relationships"`
	Actions          []ActionEligibility  `json:"actions,omitempty"`
}
type ServiceQuery struct {
	Search string
	State  string
	Offset int
	Limit  int
}
type ServicePage struct {
	Items      []Service `json:"items"`
	Total      int       `json:"total"`
	NextOffset *int      `json:"next_offset,omitempty"`
}
type UnitConnection interface {
	ListUnitsByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitStatus, error)
	ListUnitFilesByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitFile, error)
	ListUnitsByNamesContext(context.Context, []string) ([]systemdbus.UnitStatus, error)
	GetUnitPropertiesContext(context.Context, string) (map[string]any, error)
	GetUnitTypePropertiesContext(context.Context, string, string) (map[string]any, error)
	Close()
}
type ServiceProvider struct {
	Open           func(context.Context) (UnitConnection, error)
	SystemdRuntime string
	Policy         ServicePolicy
	UnitTrust      func(string) bool
}

func NewServiceProvider() ServiceProvider {
	return ServiceProvider{Open: func(ctx context.Context) (UnitConnection, error) { return systemdbus.NewSystemConnectionContext(ctx) }, SystemdRuntime: "/run/systemd/system", Policy: NewServicePolicy(false, ""), UnitTrust: trustedUnitDefinition}
}
func ValidUnit(name string) bool { return unitPattern.MatchString(name) }
func (p ServiceProvider) connect(ctx context.Context) (UnitConnection, error) {
	if p.SystemdRuntime == "" {
		return nil, ErrUnsupported
	}
	if _, e := os.Stat(p.SystemdRuntime); e != nil {
		if os.IsNotExist(e) {
			return nil, ErrUnsupported
		}
		return nil, serviceBackendError(e)
	}
	c, e := p.Open(ctx)
	if e != nil {
		return nil, serviceBackendError(e)
	}
	return c, nil
}
func (p ServiceProvider) List(ctx context.Context, q ServiceQuery) (ServicePage, error) {
	if q.Limit < 1 || q.Limit > MaxServicePage || q.Offset < 0 || q.Offset > MaxServiceInventory || len(q.Search) > 100 || !validServiceFilter(q.State) {
		return ServicePage{}, ErrInvalid
	}
	c, e := p.connect(ctx)
	if e != nil {
		return ServicePage{}, e
	}
	defer c.Close()
	files, e := c.ListUnitFilesByPatternsContext(ctx, nil, []string{"*.service"})
	if e != nil {
		return ServicePage{}, serviceBackendError(e)
	}
	loaded, e := c.ListUnitsByPatternsContext(ctx, nil, []string{"*.service"})
	if e != nil {
		return ServicePage{}, serviceBackendError(e)
	}
	if len(files) > MaxServiceInventory || len(loaded) > MaxServiceInventory {
		return ServicePage{}, ErrUnavailable
	}
	all := map[string]Service{}
	for _, f := range files {
		name := filepath.Base(f.Path)
		if !ValidUnit(name) {
			continue
		}
		all[name] = Service{Name: name, Canonical: name, Loaded: "not_loaded", Active: "inactive", Sub: "dead", Enabled: boundedText(f.Type, 64), UnitFile: boundedText(f.Path, 512), UnitTrusted: p.trust()(f.Path)}
	}
	for _, u := range loaded {
		if !ValidUnit(u.Name) {
			continue
		}
		v := all[u.Name]
		v.Name = u.Name
		v.Description = boundedText(u.Description, 256)
		v.Loaded = boundedText(u.LoadState, 64)
		v.Active = boundedText(u.ActiveState, 64)
		v.Sub = boundedText(u.SubState, 64)
		if v.Enabled == "" {
			v.Enabled = "unknown"
		}
		all[u.Name] = v
	}
	if len(all) > MaxServiceInventory {
		return ServicePage{}, ErrUnavailable
	}
	names := make([]string, 0, len(all))
	search := strings.ToLower(q.Search)
	for name, v := range all {
		if search != "" && !strings.Contains(strings.ToLower(name+" "+v.Description), search) {
			continue
		}
		if q.State == "eligible" {
			if _, reviewed := p.Policy.Allowed[name]; !reviewed {
				continue
			}
			if props, err := c.GetUnitPropertiesContext(ctx, name); err == nil {
				applyUnitMetadata(&v, props, p.trust())
				all[name] = v
			}
		}
		if !serviceMatchesFilter(v, q.State, p.Policy) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	page := ServicePage{Items: []Service{}, Total: len(names)}
	if q.Offset >= len(names) {
		return page, nil
	}
	end := q.Offset + q.Limit
	if end > len(names) {
		end = len(names)
	}
	for _, name := range names[q.Offset:end] {
		v := all[name]
		if v.Loaded != "not_loaded" {
			if props, err := c.GetUnitTypePropertiesContext(ctx, name, "Service"); err == nil {
				v.MainPID = uint32Value(props["MainPID"])
				v.Failure = failure(props)
				v.CanReload, _ = props["CanReload"].(bool)
			}
		}
		if props, err := c.GetUnitPropertiesContext(ctx, name); err == nil {
			applyUnitMetadata(&v, props, p.trust())
		}
		p.Policy.Assess(&v)
		page.Items = append(page.Items, v)
	}
	if end < len(names) {
		page.NextOffset = &end
	}
	return page, nil
}
func (p ServiceProvider) Detail(ctx context.Context, name string) (Service, error) {
	if !ValidUnit(name) {
		return Service{}, ErrInvalid
	}
	c, e := p.connect(ctx)
	if e != nil {
		return Service{}, e
	}
	defer c.Close()
	loaded, e := c.ListUnitsByNamesContext(ctx, []string{name})
	if e != nil {
		return Service{}, serviceBackendError(e)
	}
	files, e := c.ListUnitFilesByPatternsContext(ctx, nil, []string{name})
	if e != nil {
		return Service{}, serviceBackendError(e)
	}
	v := Service{Name: name, Loaded: "not_loaded", Active: "inactive", Sub: "dead", Enabled: "unknown"}
	var aliases []string
	found := false
	for _, f := range files {
		if filepath.Base(f.Path) == name {
			v.Enabled = boundedText(f.Type, 64)
			found = true
			break
		}
	}
	for _, u := range loaded {
		if u.Name == name && u.LoadState != "not-found" {
			v.Description = boundedText(u.Description, 256)
			v.Loaded = boundedText(u.LoadState, 64)
			v.Active = boundedText(u.ActiveState, 64)
			v.Sub = boundedText(u.SubState, 64)
			found = true
			break
		}
	}
	if !found {
		return Service{}, ErrMissing
	}
	props, propsErr := c.GetUnitPropertiesContext(ctx, name)
	if propsErr == nil {
		applyUnitMetadata(&v, props, p.trust())
		aliases = stringSlice(props["Names"])
		if id, ok := props["Id"].(string); ok && ValidUnit(id) {
			v.Name = id
		}
	}
	if v.Loaded != "not_loaded" {
		service, err := c.GetUnitTypePropertiesContext(ctx, v.Name, "Service")
		if err == nil {
			v.MainPID = uint32Value(service["MainPID"])
			v.Failure = failure(service)
			if canReload, ok := service["CanReload"].(bool); ok {
				v.CanReload = canReload
			}
		}
	}
	v.Aliases = aliases
	p.Policy.Assess(&v)
	for _, action := range serviceActions {
		eligibility, ok := FindEligibility(v.Actions, action)
		if ok && eligibility.Allowed {
			v.Actions = denyForGraph(v.Actions, action, graphConcerns(ctx, c, p.Policy, v, action))
		}
	}
	return v, nil
}

func (p ServiceProvider) Preview(ctx context.Context, name string, action ServiceAction) (ServiceActionPreview, error) {
	if !ValidUnit(name) || !ValidServiceAction(action) {
		return ServiceActionPreview{}, ErrInvalid
	}
	service, err := p.Detail(ctx, name)
	if err != nil {
		return ServiceActionPreview{}, err
	}
	eligibility, ok := FindEligibility(service.Actions, action)
	if !ok {
		return ServiceActionPreview{}, ErrUnavailable
	}
	return ServiceActionPreview{Unit: service.Name, Action: action, Current: service.Active, Eligibility: eligibility}, nil
}

func validServiceFilter(value string) bool {
	switch value {
	case "all", "active", "inactive", "failed", "eligible", "protected", "custom":
		return true
	}
	return false
}

func serviceMatchesFilter(v Service, filter string, policy ServicePolicy) bool {
	if filter == "all" {
		return true
	}
	if filter == "active" || filter == "inactive" || filter == "failed" {
		return v.Active == filter
	}
	policy.Assess(&v)
	switch filter {
	case "eligible":
		for _, a := range v.Actions {
			if a.Allowed {
				return true
			}
		}
		return false
	case "protected":
		return v.Management == "protected"
	case "custom":
		return strings.HasPrefix(v.UnitFile, "/etc/systemd/system/") || v.Category == CategoryCustom
	}
	return false
}

func (p ServiceProvider) trust() func(string) bool {
	if p.UnitTrust != nil {
		return p.UnitTrust
	}
	return trustedUnitDefinition
}

func applyUnitMetadata(v *Service, props map[string]any, trust func(string) bool) {
	v.MetadataComplete = true
	v.Dependencies = dependencies(props)
	v.Relationships = serviceRelationships(props)
	v.CanReload, _ = props["CanReload"].(bool)
	if path, ok := props["FragmentPath"].(string); ok {
		v.UnitFile = boundedText(path, 512)
		v.UnitTrusted = trust(path)
	}
	if aliases := stringSlice(props["Names"]); len(aliases) > 0 {
		v.Aliases = aliases
	}
	if id, ok := props["Id"].(string); ok && ValidUnit(id) {
		v.Name, v.Canonical = id, id
	} else {
		v.Canonical = v.Name
	}
}
func uint32Value(v any) uint32 {
	switch n := v.(type) {
	case uint32:
		return n
	case int32:
		if n > 0 {
			return uint32(n)
		}
	}
	return 0
}
func failure(props map[string]any) string {
	result, _ := props["Result"].(string)
	if result == "" || result == "success" {
		return ""
	}
	if len(result) > 100 {
		return "failure"
	}
	if status, ok := props["ExecMainStatus"].(int32); ok && status != 0 {
		return fmt.Sprintf("%s (exit status %d)", result, status)
	}
	return result
}
func dependencies(props map[string]any) []string {
	var out []string
	for _, key := range []string{"Requires", "Wants", "After"} {
		values, ok := props[key].([]string)
		if !ok {
			continue
		}
		for _, v := range values {
			if len(out) >= 32 {
				return out
			}
			if len(v) <= 200 {
				out = append(out, key+": "+v)
			}
		}
	}
	return out
}

func boundedText(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}

func serviceBackendError(err error) error {
	if err != nil {
		name := strings.ToLower(err.Error())
		if strings.Contains(name, "accessdenied") || strings.Contains(name, "permission denied") || strings.Contains(name, "not authorized") {
			return ErrPermission
		}
	}
	return ErrUnavailable
}
