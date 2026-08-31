package internal

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Задача 9: провайдеры добавляются файлом data/providers.json без кода.
// Тест использует временную директорию и перезагружает глобальное состояние.
func TestDynamicProvidersJSON(t *testing.T) {
	dir := t.TempDir()
	oldPath := providersJSONPath
	providersJSONPath = filepath.Join(dir, "providers.json")
	t.Cleanup(func() { providersJSONPath = oldPath; dynProvInit = sync.Once{}; dynProvSpec = nil; dynProvMod.Store(0) })
	// Сбрасываем глобальное состояние, чтобы тест не зависел от порядка.
	dynProvInit = sync.Once{}
	dynProvSpec = nil
	dynProvMod.Store(0)

	js := `// строки с # и // игнорируются (удобно для комментариев)
[
  { "name": "MyPetBooru", "title": "My Pet Booru",
    "api_url": "https://pets.example/index.php", "www_url": "https://pets.example/",
    "media_hosts": ["pets.example"] },
  { "name": "rule34", "title": "duplicate" }  // конфликт со встроенным
]
`
	if err := os.WriteFile(providersJSONPath, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	specs := loadDynamicProviderSpecs()
	if len(specs) != 1 {
		t.Fatalf("want 1 dynamic spec, got %d", len(specs))
	}
	s := specs[0]
	if s.name != "mypetbooru" || s.title != "My Pet Booru" {
		t.Fatalf("bad spec: %+v", s)
	}
	if s.apiURL != "https://pets.example/index.php" || len(s.mediaHosts) != 1 {
		t.Fatalf("bad URL/hosts: %+v", s)
	}
	// Дефолт для cache_file/max_query_len.
	if s.maxQueryLen <= 0 || s.cacheFile == "" {
		t.Fatalf("defaults not applied: %+v", s)
	}
}

func TestDynamicProvidersFrontendList(t *testing.T) {
	dir := t.TempDir()
	oldPath := providersJSONPath
	providersJSONPath = filepath.Join(dir, "providers.json")
	t.Cleanup(func() { providersJSONPath = oldPath; dynProvInit = sync.Once{}; dynProvSpec = nil; dynProvMod.Store(0) })
	dynProvInit = sync.Once{}
	dynProvSpec = nil
	dynProvMod.Store(0)
	os.WriteFile(providersJSONPath, []byte(`[{ "name": "mysite", "title": "My Site", "api_url": "https://x/index.php", "www_url": "https://x/", "media_hosts": ["x"] }]`), 0o644)

	list := allProviderDescriptors()
	found := false
	for _, p := range list {
		if p.Name == "mysite" && p.DisplayName == "My Site" {
			found = true
		}
	}
	if !found {
		t.Fatal("dynamic provider missing from settings list")
	}
	if !isKnownProvider("mysite") {
		t.Fatal("isKnownProvider should accept dynamic provider")
	}
	if isKnownProvider("doesnotexist") {
		t.Fatal("unknown provider accepted")
	}
}