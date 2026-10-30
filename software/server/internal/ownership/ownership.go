package ownership

import "fmt"

const LabelManaged = "com.kitpro.managed"
const LabelInstance = "com.kitpro.instance"
const LabelResource = "com.kitpro.resource"

func Names(instance string) (string, string, error) {
	if len(instance) < 6 || len(instance) > 64 {
		return "", "", fmt.Errorf("invalid instance id")
	}
	for _, r := range instance {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return "", "", fmt.Errorf("invalid instance id")
		}
	}
	return "kitpro-test-" + instance, "kitpro-net-" + instance, nil
}
