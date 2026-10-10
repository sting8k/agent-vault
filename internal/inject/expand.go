package inject

import "errors"

// Expand fills the {{NAME}} and {{NAME.field}} references of tmpl from src, with the same grammar
// and single pass as Build. It is for text agv itself uses, such as a webhook URL, so it writes
// no temp files and {{file:...}} is an error. Error messages carry names only, never values.
func Expand(tmpl string, src Source) (string, error) {
	for _, m := range placeholder.FindAllStringSubmatchIndex(tmpl, -1) {
		if m[2] >= 0 {
			return "", errors.New("{{file:...}} references cannot be used here")
		}
	}
	b := &builder{src: src, entries: map[string]map[string]Field{}, used: map[string]bool{}, files: map[string]string{}}
	return b.expand(tmpl)
}
