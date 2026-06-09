// Package tlddata embeds TLD data files for use by internal/tlds.
package tlddata

import _ "embed"

//go:embed public_suffix_list.dat
var PSLData []byte

//go:embed default.txt
var DefaultData []byte

//go:embed identity_digital.txt
var IdentityDigitalData []byte

//go:embed classic.txt
var ClassicData []byte

//go:embed tech.txt
var TechData []byte
