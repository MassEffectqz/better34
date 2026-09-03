package internal

import "strings"

// resolveTagSafe — ResolveTag с защитой от неинициализированной БД:
// вызывается на горячем пути построения запроса, где postDB может быть
// ещё не создан (тесты/утилиты). Возвращает тег без изменений.
func resolveTagSafe(tag string) string {
	if tag == "" || !DBReady() {
		return tag
	}
	return GetDB().ResolveTag(tag)
}

// resolveAliases переводит синонимы в канонические теги внутри строки поиска.
// Резолвер передаётся параметром — чистую логику можно тестировать без БД.
func resolveAliases(q string, resolver func(string) string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return q
	}
	for i, f := range fields {
		trimmed := strings.TrimLeft(f, "+-")
		if trimmed == "" {
			continue
		}
		// Метатеги с двоеточием (id:N, rating:sfw) и чередование (a|b)
		// провайдеру и так понятны — резолвить их не нужно.
		if strings.ContainsAny(trimmed, ":|") {
			continue
		}
		prefix := f[:len(f)-len(trimmed)]
		if resolved := resolver(trimmed); resolved != "" && resolved != trimmed {
			fields[i] = prefix + resolved
		}
	}
	return strings.Join(fields, " ")
}

// ResolveAliasesInQuery заменяет алиасы тегов (синонимы, danbooru-style) в
// строке поиска на канонические теги, сохраняя префиксы +/- и разделение
// пробелами. Использует таблицу tag_aliases глобальной БД.
func ResolveAliasesInQuery(q string) string {
	return resolveAliases(q, resolveTagSafe)
}
