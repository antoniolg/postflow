package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func preferredUILanguage(rawAcceptLanguage string) string {
	type candidate struct {
		lang  string
		q     float64
		order int
	}
	best := candidate{lang: "en", q: -1, order: 1 << 30}
	parts := strings.Split(rawAcceptLanguage, ",")
	for index, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		langTag := token
		q := 1.0
		if semi := strings.Index(token, ";"); semi >= 0 {
			langTag = strings.TrimSpace(token[:semi])
			params := strings.Split(token[semi+1:], ";")
			for _, param := range params {
				p := strings.TrimSpace(param)
				if len(p) < 3 || !strings.HasPrefix(strings.ToLower(p), "q=") {
					continue
				}
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(p[2:]), 64); err == nil {
					q = parsed
				}
			}
		}
		lang := normalizeUILanguage(langTag)
		if lang == "" {
			continue
		}
		if q > best.q || (q == best.q && index < best.order) {
			best = candidate{lang: lang, q: q, order: index}
		}
	}
	return best.lang
}

func normalizeUILanguage(rawTag string) string {
	tag := strings.ToLower(strings.TrimSpace(rawTag))
	if tag == "" {
		return ""
	}
	if tag == "*" {
		return "en"
	}
	if cut := strings.IndexAny(tag, "-_"); cut >= 0 {
		tag = tag[:cut]
	}
	switch tag {
	case "en", "es":
		return tag
	default:
		return ""
	}
}

func uiMessage(lang, key string, args ...any) string {
	catalog := uiMessages["en"]
	if specific, ok := uiMessages[lang]; ok {
		catalog = specific
	}
	msg, ok := catalog[key]
	if !ok {
		msg = uiMessages["en"][key]
	}
	if msg == "" {
		msg = key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

func localizedCalendarMonthLabel(localDate time.Time, lang string) string {
	monthNames := []string{
		"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December",
	}
	if lang == "es" {
		monthNames = []string{
			"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio",
			"Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre",
		}
	}
	index := int(localDate.Month()) - 1
	if index < 0 || index >= len(monthNames) {
		return strings.ToUpper(localDate.Format("January 2006"))
	}
	return strings.ToUpper(fmt.Sprintf("%s %d", monthNames[index], localDate.Year()))
}

func localizedSelectedDayLabel(localDate time.Time, lang string) string {
	weekdayShort := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	monthShort := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	if lang == "es" {
		weekdayShort = []string{"Dom", "Lun", "Mar", "Mie", "Jue", "Vie", "Sab"}
		monthShort = []string{"Ene", "Feb", "Mar", "Abr", "May", "Jun", "Jul", "Ago", "Sep", "Oct", "Nov", "Dic"}
	}
	weekdayIdx := int(localDate.Weekday())
	monthIdx := int(localDate.Month()) - 1
	if weekdayIdx < 0 || weekdayIdx >= len(weekdayShort) || monthIdx < 0 || monthIdx >= len(monthShort) {
		return strings.ToUpper(localDate.Format("Mon 02 Jan 2006"))
	}
	return strings.ToUpper(fmt.Sprintf("%s %02d %s %d", weekdayShort[weekdayIdx], localDate.Day(), monthShort[monthIdx], localDate.Year()))
}

func weekdayHeaders(lang string) []string {
	if lang == "es" {
		return []string{"Lun", "Mar", "Mie", "Jue", "Vie", "Sab", "Dom"}
	}
	return []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
}

func datePickerMonthLabels(lang string) []string {
	if lang == "es" {
		return []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
	}
	return []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
}

func datePickerWeekdayLabels(lang string) []string {
	if lang == "es" {
		return []string{"L", "M", "X", "J", "V", "S", "D"}
	}
	return []string{"M", "T", "W", "T", "F", "S", "S"}
}
