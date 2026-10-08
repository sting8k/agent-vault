package cli

import (
	"fmt"

	"github.com/sting8k/agent-vault/internal/skill"
)

// cmdSkills prints the embedded SKILL.md. Extra arguments are ignored: whatever an agent
// types after `agv skills`, it should end up with the instructions.
func cmdSkills(args []string, sys IO) int {
	fmt.Fprint(sys.Stdout, skill.Text)
	return 0
}
