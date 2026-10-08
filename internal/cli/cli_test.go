package cli

import "testing"

// The release workflow stamps the version through main; --version must report it.
func TestVersionFlagReportsTheStampedVersion(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	Version = "v9.9.9-test"
	r := agv(t, newHome(t), nil, "--version")
	if r.code != 0 || r.out != "agv v9.9.9-test\n" || r.err != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.out, r.err)
	}
}
