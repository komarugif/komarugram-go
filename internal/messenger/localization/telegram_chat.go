package localization

import (
	"strings"

	"komarugram/internal/messenger/model"
)

// Format substitutes named Telegram placeholders as data, never as printf syntax.
func (c Catalog) Format(key string, args map[string]string) string {
	return replaceTags(c.T(key), args)
}
func replaceTags(s string, args map[string]string) string {
	var pairs []string
	for k, v := range args {
		pairs = append(pairs, "{"+k+"}", v)
	}
	if len(pairs) > 0 {
		s = strings.NewReplacer(pairs...).Replace(s)
	}
	return s
}
func (c Catalog) SharedCount(kind model.SharedKind, n int) string {
	return c.Count("shared.count."+string(kind), n, nil)
}
func (c Catalog) SharedEmpty(kind model.SharedKind) string {
	key := "shared.empty." + string(kind)
	if telegramKey(key) != "" {
		return c.T(key)
	}
	return c.T("shared.empty")
}
