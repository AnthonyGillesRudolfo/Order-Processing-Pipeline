package uuid

import "github.com/google/uuid"

// GenerateUUID returns a random UUID string. This minimal implementation keeps
// parity with the original helper used in upstream Hashicorp packages.
func GenerateUUID() (string, error) {
	return uuid.NewString(), nil
}
