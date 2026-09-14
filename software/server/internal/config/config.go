package config

// Environment-backed configuration is intentionally small for the first slice.
type Config struct {
	APIUID                                         uint32
	HelperSocket, HelperDB, ControlDB, StorageRoot string
}
