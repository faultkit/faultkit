// Package extension defines the seam external packages use to add
// behavior to faultkit. The OSS code consumes capabilities through
// these interfaces; concrete implementations live elsewhere.
package extension

// Capability is the base interface every extension implements.
type Capability interface {
	Name() string
}
