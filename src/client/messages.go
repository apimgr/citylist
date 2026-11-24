package main

import (
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
)

func clientText(lang, key, fallback string) string {
	return clientTextFormat(lang, key, fallback, nil)
}

func clientTextFormat(lang, key, fallback string, values map[string]string) string {
	message := i18n.Translate(lang, "cli."+key)
	if message == "cli."+key {
		message = fallback
	}
	for name, value := range values {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}
