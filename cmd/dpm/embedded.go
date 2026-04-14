package main

import "embed"

// Catalog and profile YAML files are embedded at build time.
// The engine uses these when no local catalog/ or profiles/ directory exists.

//go:embed catalog
var EmbeddedCatalog embed.FS

//go:embed profiles
var EmbeddedProfiles embed.FS
