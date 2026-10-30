package hostadmin

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
)

type SystemdNetworkRecoveryScheduler struct {
	UnitDir, HelperPath string
	allowTestOwnership  bool
	Reload              func(context.Context) error
	Start               func(context.Context, string) error
	Stop                func(context.Context, string) error
}

func NewSystemdNetworkRecoveryScheduler() *SystemdNetworkRecoveryScheduler {
	s := &SystemdNetworkRecoveryScheduler{UnitDir: "/run/systemd/system", HelperPath: "/usr/libexec/kitpro-helper"}
	s.Reload = func(ctx context.Context) error {
		conn, err := systemdbus.NewSystemConnectionContext(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		return conn.ReloadContext(ctx)
	}
	s.Start = func(ctx context.Context, name string) error { return persistentSystemdJob(ctx, name, true) }
	s.Stop = func(ctx context.Context, name string) error { return persistentSystemdJob(ctx, name, false) }
	return s
}
func networkRecoveryUnit(id, kind string) string { return "kitpro-network-rollback-" + id + "." + kind }
func (s *SystemdNetworkRecoveryScheduler) paths(id string) (string, string) {
	return filepath.Join(s.UnitDir, networkRecoveryUnit(id, "service")), filepath.Join(s.UnitDir, networkRecoveryUnit(id, "timer"))
}
func (s *SystemdNetworkRecoveryScheduler) Arm(ctx context.Context, id string, deadline time.Time) error {
	if ValidateNetworkTransactionID(id) != nil || s.Reload == nil || s.Start == nil {
		return ErrInvalid
	}
	remaining := time.Until(deadline)
	if remaining < time.Second {
		return ErrInvalid
	}
	servicePath, timerPath := s.paths(id)
	service := []byte("[Unit]\nDescription=KITPro network rollback " + id + "\n\n[Service]\nType=oneshot\nAppArmorProfile=/usr/libexec/kitpro-helper\nExecStart=" + s.HelperPath + " --network-recover " + id + "\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=strict\nReadWritePaths=/etc/systemd/network /run/systemd/system /var/lib/kitpro-helper/network-transactions\n")
	timer := []byte("[Unit]\nDescription=KITPro network rollback deadline " + id + "\n\n[Timer]\nOnActiveSec=" + strconv.Itoa(int(remaining.Seconds())) + "s\nAccuracySec=1s\nUnit=" + networkRecoveryUnit(id, "service") + "\n\n[Install]\nWantedBy=timers.target\n")
	if err := atomicRootFile(servicePath, service, 0600, false, s.allowTestOwnership); err != nil {
		return err
	}
	if err := atomicRootFile(timerPath, timer, 0600, false, s.allowTestOwnership); err != nil {
		_ = os.Remove(servicePath)
		return err
	}
	if err := s.Reload(ctx); err != nil {
		_ = os.Remove(servicePath)
		_ = os.Remove(timerPath)
		return err
	}
	return s.Start(ctx, networkRecoveryUnit(id, "timer"))
}
func (s *SystemdNetworkRecoveryScheduler) Disarm(ctx context.Context, id string) error {
	if ValidateNetworkTransactionID(id) != nil {
		return ErrInvalid
	}
	if s.Stop != nil {
		_ = s.Stop(ctx, networkRecoveryUnit(id, "timer"))
	}
	servicePath, timerPath := s.paths(id)
	for _, path := range []string{timerPath, servicePath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if s.Reload != nil {
		return s.Reload(ctx)
	}
	return nil
}

func NetworkdReload(ctx context.Context) error {
	return networkdCall(ctx, "org.freedesktop.network1.Manager.Reload")
}
func NetworkdReconfigure(ctx context.Context, name string) error {
	if !ValidInterfaceName(name) {
		return ErrInvalid
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ErrUnavailable
	}
	return networkdCall(ctx, "org.freedesktop.network1.Manager.ReconfigureLink", int32(iface.Index))
}
func ObserveInterfaceAddresses(name string) ([]string, error) {
	if !ValidInterfaceName(name) {
		return nil, ErrInvalid
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, ErrUnavailable
	}
	values, err := iface.Addrs()
	if err != nil {
		return nil, ErrUnavailable
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.String())
	}
	return result, nil
}
func networkdCall(ctx context.Context, method string, args ...any) error {
	conn, err := godbus.SystemBusPrivate()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.Auth(nil); err != nil {
		return err
	}
	if err = conn.Hello(); err != nil {
		return err
	}
	call := conn.Object("org.freedesktop.network1", godbus.ObjectPath("/org/freedesktop/network1")).CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		return fmt.Errorf("networkd operation unavailable: %w", call.Err)
	}
	return nil
}
