//go:build !linux && !darwin && !windows

package collect

import "github.com/towertail/sampler/pkg/schema"

func healthPlatform(h *schema.HealthInfo) []string { return nil }
