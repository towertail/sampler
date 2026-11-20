//go:build !linux

package collect

func stableMachineID(hostID string) string { return hostID }
