package reconciliation

// Classification is deliberately conservative: ambiguous ownership is never repaired automatically.
type Classification string

const (
	Healthy       Classification = "healthy"
	Missing       Classification = "missing"
	Conflict      Classification = "ownership-conflict"
	SecurityDrift Classification = "security-drift"
)

func Classify(expected, observed string, owned bool) Classification {
	if !owned {
		return Conflict
	}
	if expected == "" || observed == "" {
		return Missing
	}
	if expected != observed {
		return SecurityDrift
	}
	return Healthy
}
