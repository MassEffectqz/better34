package internal

import (
	"os"
	"path/filepath"
	"testing"
)

// Миграция разделения лайков: в профиле, сохранённом ДО появления OwnLikes,
// поле отсутствует, а лайки есть. Раньше обмен считал их чужими и удалял (у
// реального пользователя так пропали все 555). load() обязан пометить всё
// находящееся в LikedPosts как «своё» — иначе первый же обмен сносит историю.
func TestProfileLoadMigratesOwnLikesFromLikedPosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	// Профиль старого формата: liked_posts есть, own_likes — нет.
	if err := os.WriteFile(path, []byte(`{"liked_posts":{"10":true,"20":true,"30":true},"liked_at":{"10":5}}`), 0o644); err != nil {
		t.Fatalf("записать profile.json: %v", err)
	}
	p := NewProfile(path)
	for _, id := range []int{10, 20, 30} {
		if !p.OwnLikes[id] {
			t.Errorf("лайк %d не помечен как свой: OwnLikes=%v", id, p.OwnLikes)
		}
	}
	if len(p.OwnLikes) != 3 {
		t.Errorf("OwnLikes = %v, ждали ровно 3 записи", p.OwnLikes)
	}
}

// Миграция идемпотентна и не трогает профили, где лайков нет: пустой OwnLikes
// после загрузки не должен «разрастаться» ничем.
func TestProfileLoadOwnLikesMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"liked_posts":null}`), 0o644); err != nil {
		t.Fatalf("записать profile.json: %v", err)
	}
	p := NewProfile(path)
	if len(p.OwnLikes) != 0 {
		t.Errorf("OwnLikes = %v, ждали пусто", p.OwnLikes)
	}
	p.ToggleLike(5)
	p.mu.Lock()
	n := len(p.OwnLikes)
	p.mu.Unlock()
	if n != 1 || !p.OwnLikes[5] {
		t.Errorf("лайк, поставленный после миграции, не помечен: %v", p.OwnLikes)
	}
}

// Регрессия: в profile.json может не быть карты или в ней может лежать null
// (старый файл, правка руками, обрезанная запись). json.Unmarshal оставляет
// поле nil, а запись в nil-карту в Go — паника: лайк/скрытие/избранный тег
// падали с 500. load() обязан вернуть карты инициализированными.
func TestProfileLoadInitializesMissingMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"liked_posts":null,"hidden_posts":null}`), 0o644); err != nil {
		t.Fatalf("записать profile.json: %v", err)
	}
	p := NewProfile(path)

	// Любая запись в каждую карту — при nil здесь была бы паника теста.
	p.mu.Lock()
	p.LikedPosts[1] = true
	p.OwnLikes[1] = true
	p.FriendLikes[2] = true
	p.LikedAt[1] = 100
	p.HiddenPosts[3] = true
	p.FavTags["cat"] = true
	p.HiddenTags["dog"] = true
	p.RecDisliked["tag"] = 1
	p.mu.Unlock()

	if err := p.Save(); err != nil {
		t.Fatalf("сохранить: %v", err)
	}
}

// Миграция старого единого профиля в аккаунт первого пользователя: копирование
// nil-полей из файла не должно обнулять карты профиля аккаунта — иначе первый
// же лайк нового пользователя паникует. Путь к legacy-файлу константный и
// относительный, поэтому тест уходит в tmp-каталог и возвращает CWD обратно.
func TestMigrateLegacyProfileKeepsMapsNonNil(t *testing.T) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.MkdirAll(filepath.Dir(legacyProfileFile), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	legacy := `{"liked_posts":null,"hidden_posts":null,"fav_tags":null,"hidden_tags":null}`
	if err := os.WriteFile(legacyProfileFile, []byte(legacy), 0o644); err != nil {
		t.Fatalf("записать legacy-профиль: %v", err)
	}

	a := newAccounts(filepath.Join(dir, "accounts"))
	a.migrateLegacyProfile("tester")

	p := a.Profile("tester")
	p.mu.Lock()
	p.LikedPosts[1] = true
	p.OwnLikes[1] = true
	p.HiddenPosts[2] = true
	p.FavTags["x"] = true
	p.HiddenTags["y"] = true
	p.mu.Unlock()

	if _, err := os.Stat(legacyProfileFile + ".bak"); err != nil {
		t.Errorf("исходный профиль должен остаться в .bak: %v", err)
	}
	if _, err := os.Stat(legacyProfileFile); !os.IsNotExist(err) {
		t.Errorf("исходный профиль должен быть переименован, stat=%v", err)
	}
}
