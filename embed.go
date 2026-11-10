package main

import "embed"

// assets holds the vault scaffold, the skills and the export scaffold (outside scaffold/, which init copies into every vault); all: keeps dotfile dirs (.zk, .zk-chef, .githooks).
//
//go:embed all:scaffold skills export-scaffold
var assets embed.FS
