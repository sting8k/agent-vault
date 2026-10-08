# D-je05 Go CLI agv, macOS and Linux first, stdlib plus x/term

<!-- Status lives in .harness/decisions/D-je05.json. Change it with `just-harness-cli decision update`. -->

## Context

agv must ship as one easy-to-install binary for agents on developer machines; Windows is wanted later.

## Decision

Go, one binary named `agv`. macOS and Linux in v1; platform code behind build tags so Windows can follow. Dependencies: standard library plus `golang.org/x/term`.

## Consequences

`os/exec` gives safe process spawning on every target. Go cannot reliably zero secret memory (GC copies); accepted, since the goal is accidental exposure, not memory forensics.
