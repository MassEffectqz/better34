package internal

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// нужен только при отладке ротации ключей: в обычном режиме он пишет
// строку на каждый API-запрос и захламляет журнал.
var logAPIKeyDebug = os.Getenv("BRIEFLY_DEBUG_API") == "1"

// keyManager адаптивно распределяет нагрузку между API-ключами:
// следит за здоровьем каждого, сажает сбоящие ключи на карантин,
// автоматически подхватывает новые ключи из конфига и отдаёт
// больным ключам меньше запросов (EWMA ошибок + LRU по использованию).
const (
	keyExclude403       = 30 * time.Minute
	keyExcludeTransient = 10 * time.Minute
	keyMaxConsecFails   = 3
	keyEWMADecay        = 0.9
	keyEWMAFailShare    = 0.3
)

type keyState struct {
	cred          APICredential
	fails         int
	ewma          float64
	lastSeq       uint64
	excludedUntil time.Time
	excludedWhy   string
}

type keyManager struct {
	mu      sync.Mutex
	keys    []keyState
	lastGen uint64
	seq     uint64
}

func credentialsForSite(creds []APICredential, site string) []APICredential {
	var out []APICredential
	for _, c := range creds {
		p := strings.ToLower(strings.TrimSpace(c.Provider))
		if p == "" || p == site {
			out = append(out, c)
		}
	}
	return out
}

// syncFromConfig подхватывает изменения конфига (новые/удалённые ключи)
// без рестарта: пересчитывает список только если конфиг реально
// перечитался (изменилось поколение). У каждого провайдера свой
// экземпляр keyManager, поэтому сайт здесь не отслеживается.
// Ключи фильтруются по сайту: cred.Provider пустой → подходят всем,
// иначе ключ отдаётся только своему провайдеру.
func (m *keyManager) syncFromConfig(site string) {
	gen := configGen.Load()
	// lastGen проверяется и мутируется под тем же локом, что и keys:
	// параллельные вызовы из fan-out запросов иначе дают DATA RACE.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastGen == gen {
		return
	}
	m.lastGen = gen
	m.syncLocked(credentialsForSite(GetConfig().GetAPICredentials(), site))
}

func (m *keyManager) sync(creds []APICredential) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncLocked(creds)
}

// healthyCount — сколько ключей сейчас не на карантине. Нужен тестам
// ротации. U1000 использование из тестов не видит — см. check:static.
func (m *keyManager) healthyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	for i := range m.keys {
		if now.After(m.keys[i].excludedUntil) {
			n++
		}
	}
	return n
}
func (m *keyManager) syncLocked(creds []APICredential) {
	byKey := make(map[string]*keyState, len(m.keys))
	for i := range m.keys {
		byKey[m.keys[i].cred.APIKey] = &m.keys[i]
	}
	newList := make([]keyState, 0, len(creds))
	for _, cred := range creds {
		if cred.APIKey == "" {
			continue
		}
		if st, ok := byKey[cred.APIKey]; ok {
			newList = append(newList, *st)
			continue
		}
		newList = append(newList, keyState{cred: cred})
		log.Printf("[key-manager] новый ключ ...%s подключён к ротации (всего %d)", keyTail(cred.APIKey), len(newList))
	}
	m.keys = newList
}

// pickCred выбирает лучший доступный ключ: здоровые получают нагрузку
// по очереди (LRU), сбоящие — реже или вообще не получают.
func (m *keyManager) pickCred() (APICredential, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var best *keyState
	for i := range m.keys {
		k := &m.keys[i]
		if now.Before(k.excludedUntil) {
			continue
		}
		if best == nil ||
			k.ewma < best.ewma ||
			(k.ewma == best.ewma && k.lastSeq < best.lastSeq) {
			best = k
		}
	}
	if best == nil {
		return APICredential{}, false
	}
	m.seq++
	best.lastSeq = m.seq
	return best.cred, true
}

func (m *keyManager) report(apiKey string, ok bool, why string) {
	if apiKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.keys {
		k := &m.keys[i]
		if k.cred.APIKey != apiKey {
			continue
		}
		if ok {
			k.fails = 0
			k.ewma *= keyEWMADecay
			k.excludedUntil = time.Time{}
			k.excludedWhy = ""
			return
		}
		k.fails++
		k.ewma = k.ewma*(1-keyEWMAFailShare) + keyEWMAFailShare
		switch {
		case why == "auth":
			k.excludedUntil = time.Now().Add(keyExclude403)
			k.excludedWhy = "ключ невалиден для этого сайта (401/403)"
		case k.fails >= keyMaxConsecFails:
			k.excludedUntil = time.Now().Add(keyExcludeTransient)
			k.excludedWhy = fmt.Sprintf("%d ошибок подряд", k.fails)
		}
		if !k.excludedUntil.IsZero() {
			log.Printf("[key-manager] ключ ...%s уходит на карантин на %s: %s",
				keyTail(apiKey), time.Until(k.excludedUntil).Round(time.Minute), k.excludedWhy)
		}
		return
	}
}
