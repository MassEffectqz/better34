// Р–РёРІР°СЏ РїСЂРѕРІРµСЂРєР° РѕР±РјРµРЅР° РјРµР¶РґСѓ Р”Р’РЈРњРЇ СЂРµР°Р»СЊРЅРѕ Р·Р°РїСѓС‰РµРЅРЅС‹РјРё РёРЅСЃС‚Р°РЅСЃР°РјРё.
// РџРѕРґРЅРёРјР°РµРј РґРІР° СЃРµСЂРІРµСЂР° РЅР° СЂР°Р·РЅС‹С… РїРѕСЂС‚Р°С…, СЂРµРіРёСЃС‚СЂРёСЂСѓРµРј РїРѕ Р°РєРєР°СѓРЅС‚Сѓ,
// РѕР±РјРµРЅРёРІР°РµРјСЃСЏ friend-РєРѕРґР°РјРё Рё Р¶РјС‘Рј В«РѕР±РјРµРЅСЏС‚СЊСЃСЏВ». РџСЂРѕРІРµСЂСЏРµРј, С‡С‚Рѕ Р»Р°Р№Рє,
// СЃРґРµР»Р°РЅРЅС‹Р№ РЅР° A, РґРѕРµС…Р°Р» РґРѕ B, Рё РЅР°РѕР±РѕСЂРѕС‚.
//
// Р—Р°РїСѓСЃРє: BRIEFLY_TEST_BIN=briefly.exe go test -run TestTwoInstancesSync ./internal/
// Р‘РµР· РїРµСЂРµРјРµРЅРЅРѕР№ РѕРєСЂСѓР¶РµРЅРёСЏ С‚РµСЃС‚ РїСЂРѕРїСѓСЃРєР°РµС‚СЃСЏ (РЅСѓР¶РµРЅ СЃРѕР±СЂР°РЅРЅС‹Р№ Р±РёРЅР°СЂРЅРёРє).
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
	cmd := exec.Command(bin)
	cmd.Dir = dir
	// BRIEFLY_FRIENDS_URL Р·Р°РґР°С‘С‚ Р°РґСЂРµСЃ СЏРІРЅРѕ: РІ С‚РµСЃС‚Рµ РїРѕР»Р°РіР°С‚СЊСЃСЏ РЅР°
	// Р°РІС‚РѕРѕРїСЂРµРґРµР»РµРЅРёРµ РїРѕ СЃРµС‚РµРІС‹Рј РєР°СЂС‚Р°Рј РЅРµР»СЊР·СЏ (РІ CI РёС… РјРѕР¶РµС‚ РЅРµ Р±С‹С‚СЊ).
	cmd.Env = append(os.Environ(),
		"BRIEFLY_HOST=127.0.0.1",
		"BRIEFLY_PORT="+port,
		"BRIEFLY_TLS=0",
		"BRIEFLY_FRIENDS_URL=http://127.0.0.1:"+port,
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
	if !containsID(profB.Liked, 777) {
		t.Errorf("Р»Р°Р№Рє СЃ A РЅРµ РґРѕРµС…Р°Р» РґРѕ B: %v", profB.Liked)
	}

	// РћР±СЂР°С‚РЅРѕРµ РЅР°РїСЂР°РІР»РµРЅРёРµ.
	if code := apiJSON(t, "POST", baseB+"/api/like/888", cookieB, map[string]bool{"liked": true}, nil); code != 200 {
		t.Fatalf("B: like = %d", code)
	}
	apiJSON(t, "POST", baseA+"/api/friends/sync", cookieA, nil, nil)
	var profA liveProfile
	apiJSON(t, "GET", baseA+"/api/profile", cookieA, nil, &profA)
	if !containsID(profA.Liked, 888) {
		t.Errorf("Р»Р°Р№Рє СЃ B РЅРµ РґРѕРµС…Р°Р» РґРѕ A: %v", profA.Liked)
	}
	// РќР°С€ СЃРѕР±СЃС‚РІРµРЅРЅС‹Р№ Р»Р°Р№Рє 777 РЅР° A РѕР±СЏР·Р°РЅ СѓС†РµР»РµС‚СЊ.
	if !containsID(profA.Liked, 777) {
		t.Errorf("РЅР°С€ Р»Р°Р№Рє 777 РїСЂРѕРїР°Р» РїРѕСЃР»Рµ РѕР±РјРµРЅР°: %v", profA.Liked)
	}

	// РРґРµРјРїРѕС‚РµРЅС‚РЅРѕСЃС‚СЊ: РїРѕРІС‚РѕСЂРЅС‹Р№ РѕР±РјРµРЅ РЅРµ РїР»РѕРґРёС‚ РґСѓР±Р»Рё.
	apiJSON(t, "POST", baseA+"/api/friends/sync", cookieA, nil, nil)
	apiJSON(t, "GET", baseB+"/api/profile", cookieB, nil, &profB)
	if n := countID(profB.Liked, 777); n != 1 {
		t.Errorf("РїРѕСЃР»Рµ РґРІСѓС… РѕР±РјРµРЅРѕРІ Р»Р°Р№Рє РІСЃС‚СЂРµС‡Р°РµС‚СЃСЏ %d СЂР°Р·, Р¶РґР°Р»Рё 1", n)
	}

	// РЎРїРёСЃРѕРє РґСЂСѓР·РµР№ Рё РѕС‚СЃСѓС‚СЃС‚РІРёРµ РѕС€РёР±РєРё РїРѕСЃР»Рµ СѓСЃРїРµС€РЅРѕРіРѕ РѕР±РјРµРЅР°.
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
	if code := apiJSON(t, "DELETE", baseA+"/api/friends/"+listA.Friends[0].ID, cookieA, nil, &del); code != 200 {
		t.Errorf("A: delete friend = %d", code)
	}
	apiJSON(t, "GET", baseA+"/api/friends", cookieA, nil, &listA)
	if len(listA.Friends) != 0 {
		t.Errorf("РїРѕСЃР»Рµ СѓРґР°Р»РµРЅРёСЏ РґСЂСѓР·РµР№ %d, Р¶РґР°Р»Рё 0", len(listA.Friends))
	}
}

func containsID(ids []int, want int) bool { return countID(ids, want) > 0 }

func countID(ids []int, want int) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}
