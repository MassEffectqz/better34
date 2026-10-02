package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// startInstance РїРѕРґРЅРёРјР°РµС‚ РёРЅСЃС‚Р°РЅСЃ РІ РѕС‚РґРµР»СЊРЅРѕРј РєР°С‚Р°Р»РѕРіРµ: СЃРІРѕСЏ Р‘Р”, СЃРІРѕР№ РїРѕСЂС‚.
func startInstance(t *testing.T, bin, dir, port string) {
	t.Helper()
	startInstanceURL(t, bin, dir, port, "http://127.0.0.1:"+port)
}

// startInstanceURL — то же, но с заданным адресом, который инстанс объявляет
// друзьям (BRIEFLY_FRIENDS_URL). Нужен, чтобы проверить случай, когда реальный
// адрес инстанса НЕ входит в список разрешённых хостов: подставив адрес из
// чужой VPN-сети, мы воспроизводим вид запроса от соседа по сети.
func startInstanceURL(t *testing.T, bin, dir, port, friendsURL string) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = dir
	// BRIEFLY_FRIENDS_URL задаёт адрес прямо: автоопределение в тесте может
	// подхватить адрес машины (в CI сеть может быть любой).
	cmd.Env = append(os.Environ(),
		"BRIEFLY_HOST=127.0.0.1",
		"BRIEFLY_PORT="+port,
		"BRIEFLY_TLS=0",
		"BRIEFLY_FRIENDS_URL="+friendsURL,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := friendRequest(http.MethodGet, "http://127.0.0.1:"+port+"/api/healthz", "", "", nil)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("РёРЅСЃС‚Р°РЅСЃ РЅР° РїРѕСЂС‚Сѓ %s РЅРµ РїРѕРґРЅСЏР»СЃСЏ", port)
}

// apiJSON РґС‘СЂРіР°РµС‚ API РёРЅСЃС‚Р°РЅСЃР°. РђРІС‚РѕСЂРёР·Р°С†РёСЏ вЂ” cookie-СЃРµСЃСЃРёСЏ, РїРѕСЌС‚РѕРјСѓ РїРѕСЃР»Рµ
// Р»РѕРіРёРЅР° РїРµСЂРµРґР°С‘Рј РµС‘ РІ cookie (РєР°Рє СЌС‚Рѕ РґРµР»Р°РµС‚ Р±СЂР°СѓР·РµСЂ).
func apiJSON(t *testing.T, method, url, cookie string, body, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := friendHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

// registerAndLogin СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ Р°РєРєР°СѓРЅС‚ Рё РІРѕР·РІСЂР°С‰Р°РµС‚ cookie СЃРµСЃСЃРёРё.
func registerAndLogin(t *testing.T, base, user string) string {
	t.Helper()
	body := map[string]string{"username": user, "password": "secret123"}
	if code := apiJSON(t, "POST", base+"/api/auth/register", "", body, nil); code != 200 {
		t.Fatalf("%s: register = %d", user, code)
	}
	req, _ := http.NewRequest("POST", base+"/api/auth/login", bytes.NewReader(mustJSON(t, body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := friendHTTPClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: login = %d", user, resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("%s: РІ РѕС‚РІРµС‚Рµ Р»РѕРіРёРЅР° РЅРµС‚ СЃРµСЃСЃРёРѕРЅРЅРѕР№ РєСѓРєРё", user)
	return ""
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// liveProfile РїРѕРІС‚РѕСЂСЏРµС‚ С„РѕСЂРјСѓ РѕС‚РІРµС‚Р° GET /api/profile: РїРѕР»СЏ Р»РµР¶Р°С‚ РІ РєРѕСЂРЅРµ
// (handlers_profile.go), Р±РµР· РѕР±С‘СЂС‚РєРё В«profileВ».
type liveProfile struct {
	Liked []int `json:"liked_posts"`
}

// TestFriendDoorForeignHost — то, что видел пользователь: сосед по VPN стучится
// на адрес 26.x.x.x, а тест поднимает инстанс на 127.0.0.1. Проверка Host не
// должна отсекать такой запрос раньше, чем будет предъявлен ключ.
func TestFriendDoorForeignHost(t *testing.T) {
	bin := os.Getenv("BRIEFLY_TEST_BIN")
	if bin == "" {
		t.Skip("BRIEFLY_TEST_BIN не задан — нужен собранный бинарник")
	}
	port := freePort(t)
	// Адрес из чужой VPN-сети: он заведомо не принадлежит этой машине, поэтому
	// не попадает в список разрешённых хостов. Именно из-за этого запрос от
	// соседа раньше отсекался с 403 раньше проверки ключа.
	startInstanceURL(t, bin, t.TempDir(), port, "http://26.99.99.99:"+port)
	base := "http://127.0.0.1:" + port
	cookie := registerAndLogin(t, base, "door")

	var code struct {
		Code string `json:"code"`
	}
	apiJSON(t, "GET", base+"/api/friends/code", cookie, nil, &code)
	_, key, _, err := ParseFriendCode(code.Code)
	if err != nil {
		t.Fatalf("code not parsed: %v", err)
	}
	got := friendDoorRequest(t, "GET", base+"/api/friend/share", key, "26.99.99.99:"+port, base, nil, nil)
	if got != http.StatusOK {
		t.Errorf("door with foreign Host = %d, want 200 (было 403 host not allowed)", got)
	}
}

func TestTwoInstancesSync(t *testing.T) {
	bin := os.Getenv("BRIEFLY_TEST_BIN")
	if bin == "" {
		t.Skip("BRIEFLY_TEST_BIN РЅРµ Р·Р°РґР°РЅ вЂ” РЅСѓР¶РµРЅ СЃРѕР±СЂР°РЅРЅС‹Р№ Р±РёРЅР°СЂРЅРёРє")
	}
	portA, portB := freePort(t), freePort(t)
	// Переменная могла остаться от прошлого прогона, а бинарник — исчезнуть.
	// Тогда это проблема окружения, а не кода: пропускаем, а не падаем.
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("BRIEFLY_TEST_BIN=%s не найден: %v", bin, err)
	}
	startInstance(t, bin, t.TempDir(), portA)
	startInstance(t, bin, t.TempDir(), portB)
	baseA := "http://127.0.0.1:" + portA
	baseB := "http://127.0.0.1:" + portB

	// РђРєРєР°СѓРЅС‚С‹ РЅСѓР¶РЅС‹: /api/* Р·Р°РєСЂС‹С‚ Р±РµР· РІС…РѕРґР°, Р° РґСЂСѓР·СЊСЏ С…СЂР°РЅСЏС‚СЃСЏ per-user.
	cookieA := registerAndLogin(t, baseA, "alice")
	cookieB := registerAndLogin(t, baseB, "bob")

	// Р›Р°Р№Рє РЅР° A вЂ” РѕРЅ РґРѕР»Р¶РµРЅ РґРѕР»РµС‚РµС‚СЊ РґРѕ B.
	if code := apiJSON(t, "POST", baseA+"/api/like/777", cookieA, map[string]bool{"liked": true}, nil); code != 200 {
		t.Fatalf("A: like = %d", code)
	}

	// РљРѕРґС‹ РґСЂСѓРі РґСЂСѓРіР°.
	var codeA, codeB struct {
		Code string `json:"code"`
	}
	apiJSON(t, "GET", baseA+"/api/friends/code", cookieA, nil, &codeA)
	apiJSON(t, "GET", baseB+"/api/friends/code", cookieB, nil, &codeB)
	if !strings.HasPrefix(codeA.Code, friendCodePrefix) || !strings.HasPrefix(codeB.Code, friendCodePrefix) {
		t.Fatalf("РєРѕРґС‹ РЅРµ friend-РєРѕРґС‹: %q / %q", codeA.Code, codeB.Code)
	}
	t.Logf("A: %sвЂ¦ B: %sвЂ¦", codeA.Code[:40], codeB.Code[:40])

	// Ключ B понадобится ниже: обращаясь к «двери» B, мы авторизуемся тем
	// ключом, который выдал ОН (он лежит у нас в записи друга).
	_, keyB, _, perr := ParseFriendCode(codeB.Code)
	if perr != nil {
		t.Fatalf("code B not parsed: %v", perr)
	}

	// Р’Р·Р°РёРјРЅРѕРµ РґРѕР±Р°РІР»РµРЅРёРµ.
	if code := apiJSON(t, "POST", baseA+"/api/friends", cookieA, map[string]string{"code": codeB.Code}, nil); code != 200 {
		t.Fatalf("A: add friend = %d", code)
	}
	if code := apiJSON(t, "POST", baseB+"/api/friends", cookieB, map[string]string{"code": codeA.Code}, nil); code != 200 {
		t.Fatalf("B: add friend = %d", code)
	}
	// РџРѕРІС‚РѕСЂРЅРѕРµ РґРѕР±Р°РІР»РµРЅРёРµ С‚РѕРіРѕ Р¶Рµ РєРѕРґР° вЂ” РїРѕРЅСЏС‚РЅР°СЏ РѕС€РёР±РєР°, Р° РЅРµ 500.
	if code := apiJSON(t, "POST", baseA+"/api/friends", cookieA, map[string]string{"code": codeB.Code}, nil); code == 200 {
		t.Error("РїРѕРІС‚РѕСЂРЅРѕРµ РґРѕР±Р°РІР»РµРЅРёРµ РґСЂСѓРіР° РїСЂРѕС€Р»Рѕ СѓСЃРїРµС€РЅРѕ")
	}

	// РћР±РјРµРЅ: A С‚СЏРЅРµС‚ РґР°РЅРЅС‹Рµ B Рё РѕС‚РґР°С‘С‚ СЃРІРѕРё.
	if code := apiJSON(t, "POST", baseA+"/api/friends/sync", cookieA, nil, nil); code != 200 {
		t.Fatalf("A: sync = %d", code)
	}
	var profB liveProfile
	apiJSON(t, "GET", baseB+"/api/profile", cookieB, nil, &profB)
	// Лайк с A — это лайк A: в «Моих лайках» B его быть не должно, иначе профиль
	// показывал бы вкусы друга — ровно то, на что жаловался пользователь.
	if containsID(profB.Liked, 777) {
		t.Errorf("чужой лайк 777 попал в мои лайки на B: %v", profB.Liked)
	}

	// Обратное направление.
	if code := apiJSON(t, "POST", baseB+"/api/like/888", cookieB, map[string]bool{"liked": true}, nil); code != 200 {
		t.Fatalf("B: like = %d", code)
	}
	apiJSON(t, "POST", baseA+"/api/friends/sync", cookieA, nil, nil)
	var profA liveProfile
	apiJSON(t, "GET", baseA+"/api/profile", cookieA, nil, &profA)
	if containsID(profA.Liked, 888) {
		t.Errorf("чужой лайк 888 попал в мои лайки на A: %v", profA.Liked)
	}
	// Наш собственный лайк 777 на A обязан уцелеть.
	if !containsID(profA.Liked, 777) {
		t.Errorf("наш лайк 777 пропал после обмена: %v", profA.Liked)
	}

	// Идемпотентность: повторный обмен не плодит дубли и не тащит чужое в наши
	// лайки. Лайки друга видны в ЕГО профиле (снимок), а не в моём.
	apiJSON(t, "POST", baseA+"/api/friends/sync", cookieA, nil, nil)
	apiJSON(t, "GET", baseA+"/api/profile", cookieA, nil, &profA)
	if n := countID(profA.Liked, 777); n != 1 {
		t.Errorf("после двух обменов наш лайк встречается %d раз, ждали 1", n)
	}
	if containsID(profA.Liked, 888) {
		t.Errorf("после двух обменов чужой лайк в моих: %v", profA.Liked)
	}

	// РЎРїРёСЃРѕРє РґСЂСѓР·РµР№ Рё РѕС‚СЃСѓС‚СЃС‚РІРёРµ РѕС€РёР±РєРё РїРѕСЃР»Рµ СѓСЃРїРµС€РЅРѕРіРѕ РѕР±РјРµРЅР°.
	// Вкусы друга (B): избранный тег, скрытый пост, «голоса против» и альбом.
	// Без них снимок профиля был бы пустым и проверять было бы нечего.
	apiJSON(t, "POST", baseB+"/api/fav-tag", cookieB, map[string]string{"tag": "b"}, nil)
	apiJSON(t, "POST", baseB+"/api/hide/5555", cookieB, map[string]bool{"hidden": true}, nil)
	// Два «голоса против» подряд: проверяем, что счётчик и сортировка по нему
	// доехали до A, и что «ugly» остался первым.
	apiJSON(t, "POST", baseB+"/api/recommend/dislike", cookieB, map[string]any{"tags": []string{"ugly"}}, nil)
	apiJSON(t, "POST", baseB+"/api/recommend/dislike", cookieB, map[string]any{"tags": []string{"ugly"}}, nil)
	apiJSON(t, "POST", baseB+"/api/recommend/dislike", cookieB, map[string]any{"tags": []string{"meh"}}, nil)
	var collB struct {
		ID string `json:"id"`
	}
	apiJSON(t, "POST", baseB+"/api/collection", cookieB, map[string]string{"name": "albumB"}, &collB)
	apiJSON(t, "POST", baseB+"/api/collection/"+collB.ID+"/post", cookieB, map[string]any{"post_id": 888}, nil)
	// Обмен ещё раз: снимок профиля B обновляется при нашем обмене, а вкусы B мы
	// завели после прошлого. Без этого проверка смотрела бы на устаревший снимок.
	apiJSON(t, "POST", baseA+"/api/friends/sync", cookieA, nil, nil)

	// Список друзей и отсутствие ошибки после успешного обмена.
	var listA struct {
		Friends []friendView `json:"friends"`
	}
	apiJSON(t, "GET", baseA+"/api/friends", cookieA, nil, &listA)
	if len(listA.Friends) != 1 {
		t.Fatalf("Сѓ A %d РґСЂСѓР·РµР№, Р¶РґР°Р»Рё 1", len(listA.Friends))
	}
	if listA.Friends[0].URL != baseB {
		t.Errorf("Р°РґСЂРµСЃ РґСЂСѓРіР° = %q, Р¶РґР°Р»Рё %q", listA.Friends[0].URL, baseB)
	}
	if listA.Friends[0].LastErr != "" {
		t.Errorf("РїРѕСЃР»Рµ РѕР±РјРµРЅР° РѕСЃС‚Р°Р»Р°СЃСЊ РѕС€РёР±РєР°: %q", listA.Friends[0].LastErr)
	}
	if listA.Friends[0].LastSync == "" {
		t.Error("РЅРµ Р·Р°РїРёСЃР°РЅРѕ РІСЂРµРјСЏ РїРѕСЃР»РµРґРЅРµРіРѕ РѕР±РјРµРЅР°")
	}
	// РљР»СЋС‡ вЂ” bearer, РЅР°СЂСѓР¶Сѓ РѕРЅ РЅРµ РѕС‚РґР°С‘С‚СЃСЏ.
	raw := fmt.Sprint(listA.Friends[0])
	if strings.Contains(raw, strings.Repeat("0", 32)) {
		t.Error("РІ СЃРїРёСЃРєРµ РґСЂСѓР·РµР№ РїРѕС…РѕР¶Рµ, С‡С‚Рѕ СѓС‚РµРєР°РµС‚ РєР»СЋС‡")
	}

	// РЈРґР°Р»РµРЅРёРµ РґСЂСѓРіР°: A Р±РѕР»СЊС€Рµ РЅРµ РґРѕР»Р¶РµРЅ РµРіРѕ РІРёРґРµС‚СЊ.
	var del struct {
		OK bool `json:"ok"`
	}
	// Дверь с VPN-адресом в Host (регрессия 403) + новое поведение приёма:
	// чужой push больше НЕ применяется (обмен по требованию), поэтому в ответе
	// applied:false, и подсунутый лайк не появляется ни в профиле B, ни в его
	// снимке A.
	{
		var payload struct {
			OK      bool `json:"ok"`
			Applied bool `json:"applied"`
		}
		code := friendDoorRequest(t, "POST", baseB+"/api/friend/ingest",
			keyB, "26.130.42.36:3000", baseA,
			map[string]any{
				"Version": 1, "App": "briefly", "Instance": baseA,
				"User": "alice", "Likes": []map[string]any{{"post_id": 9001, "liked_at": 1}},
			}, &payload)
		if code != http.StatusOK {
			t.Errorf("obmen s VPN-adresom v Host = %d, zhdal 200 (bylo 403)", code)
		}
		if payload.Applied {
			t.Error("чужой push применён: обмен должен идти по требованию (открытие профиля/кнопка)")
		}
		var profAfter struct {
			Liked []int `json:"liked_posts"`
		}
		apiJSON(t, "GET", baseB+"/api/profile", cookieB, nil, &profAfter)
		if containsID(profAfter.Liked, 9001) {
			t.Errorf("лайк из чужого push попал в мои лайки: %v", profAfter.Liked)
		}

		// Профиль друга у B обновляется при ОТКРЫТИИ: GET сам синхронизируется с
		// A, и в нём видны настоящие лайки A (777), а не подсунутый 9001.
		var listB struct {
			Friends []friendView `json:"friends"`
		}
		apiJSON(t, "GET", baseB+"/api/friends", cookieB, nil, &listB)
		if len(listB.Friends) == 0 {
			t.Fatal("у B нет друзей: профиль друга смотреть негде")
		}
		var friendProf struct {
			Friend struct {
				Likes []int `json:"likes"`
			} `json:"friend"`
		}
		apiJSON(t, "GET", baseB+"/api/friends/"+listB.Friends[0].ID+"/profile", cookieB, nil, &friendProf)
		if !containsID(friendProf.Friend.Likes, 777) {
			t.Errorf("настоящий лайк A (777) не виден в профиле друга после открытия: %v", friendProf.Friend.Likes)
		}
		if containsID(friendProf.Friend.Likes, 9001) {
			t.Errorf("подсунутый лайк 9001 попал в снимок: %v", friendProf.Friend.Likes)
		}
	}

	// Профиль друга: снимок его вкусов должен прийти после обмена. Без него
	// вкладка «Друзья» показывала бы пустоту, хотя обмен отработал.
	{
		var prof struct {
			Friend struct {
				Likes        []int    `json:"likes"`
				Disliked     []int    `json:"disliked"`
				FavTags      []string `json:"fav_tags"`
				DislikedTags []struct {
					Tag   string `json:"tag"`
					Count int    `json:"count"`
				} `json:"disliked_tags"`
				Collections []struct {
					Name  string `json:"name"`
					Count int    `json:"count"`
				} `json:"collections"`
				SyncedAt string `json:"synced_at"`
			} `json:"friend"`
		}
		code := apiJSON(t, "GET", baseA+"/api/friends/"+listA.Friends[0].ID+"/profile", cookieA, nil, &prof)
		if code != 200 {
			t.Fatalf("GET friend profile = %d, want 200", code)
		}
		if prof.Friend.SyncedAt == "" {
			t.Error("в профиле друга нет synced_at: снимок не сохранился")
		}
		// Лайки B в его профиле — только ЕГО лайки: 888, который он поставил сам.
		// Наш 777 сюда попадать не должен: иначе снимок приписывал бы другу наши
		// вкусы (раньше так и было — лайк «эхом» возвращался от него вместе с его
		// собственными, потому что обмен сливал лайки в один профиль).
		if !containsID(prof.Friend.Likes, 888) {
			t.Errorf("лайк друга 888 не виден в профиле: %v", prof.Friend.Likes)
		}
		if containsID(prof.Friend.Likes, 777) {
			t.Errorf("мой лайк 777 приписан другу: %v", prof.Friend.Likes)
		}
		if !containsID(prof.Friend.Disliked, 5555) {
			t.Errorf("скрытое друга (5555) не в профиле: %v", prof.Friend.Disliked)
		}
		if !containsStr(prof.Friend.FavTags, "b") {
			t.Errorf("избранный тег друга (b) не в профиле: %v", prof.Friend.FavTags)
		}
		if len(prof.Friend.DislikedTags) == 0 || prof.Friend.DislikedTags[0].Tag != "ugly" {
			t.Errorf("штрафные теги друга: %+v, ждали первый ugly", prof.Friend.DislikedTags)
		}
		if len(prof.Friend.Collections) == 0 {
			t.Error("альбомы друга не попали в профиль")
		}
	}

	if code := apiJSON(t, "DELETE", baseA+"/api/friends/"+listA.Friends[0].ID, cookieA, nil, &del); code != 200 {
		t.Errorf("A: delete friend = %d", code)
	}
	apiJSON(t, "GET", baseA+"/api/friends", cookieA, nil, &listA)
	if len(listA.Friends) != 0 {
		t.Errorf("РїРѕСЃР»Рµ СѓРґР°Р»РµРЅРёСЏ РґСЂСѓР·РµР№ %d, Р¶РґР°Р»Рё 0", len(listA.Friends))
	}
}

// friendDoorRequest стучится на «дверь» инстанса с ЧУЖИМ Host-заголовком,
// как это делает сосед по VPN-сети: он идёт на наш адрес 26.x.x.x, а мы
// отвечаем на 127.0.0.1. Именно такой запрос отсекался проверкой Host с 403 —
// старый тест гонял оба инстанса по 127.0.0.1 и этого не видел.
func friendDoorRequest(t *testing.T, method, url, key, host, from string, body any, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Briefly-Friend-Key", key)
	// Заголовок отправителя: получатель сверяет его со своим списком друзей.
	req.Header.Set("X-Briefly-Friend-From", from)
	// Host — адрес инстанса в VPN-сети, а не тот, куда мы реально подключились.
	req.Host = host
	resp, err := friendHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

func containsID(ids []int, want int) bool { return countID(ids, want) > 0 }

// containsStr ищет тег в списке (для избранных тегов в профиле друга).
func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func countID(ids []int, want int) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}
