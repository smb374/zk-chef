package main

import "embed"

// assets holds the vault scaffold and the skills; all: keeps dotfile dirs (.zk, .zk-chef, .githooks).
//
//go:embed all:scaffold skills
var assets embed.FS
