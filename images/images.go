// Package images embeds the Docker build contexts for images lpsd builds itself.
package images

import "embed"

// UbuntuVPS is the build context for the Linux VPS image.
//
//go:embed ubuntu-vps/*
var UbuntuVPS embed.FS

// UbuntuVPSTag is the local tag lpsd builds the VPS image under.
// Bump the version whenever ubuntu-vps/ changes so hosts rebuild it.
const UbuntuVPSTag = "lps/ubuntu-vps:1"
