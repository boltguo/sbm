package webembed

import "embed"

// Assets contains the production Vue build.
//
//go:embed dist/*
var Assets embed.FS
