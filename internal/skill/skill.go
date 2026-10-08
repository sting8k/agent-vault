// Package skill embeds the SKILL.md that `agv skills` prints for coding agents.
package skill

import _ "embed"

// Text is SKILL.md, frontmatter included, so it can be saved as a skill file as it is.
//
//go:embed SKILL.md
var Text string
