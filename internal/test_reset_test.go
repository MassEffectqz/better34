package internal

import (
	"sync"
	"testing"
)

// resetGlobalTestState пересоздаёт глобальные синглтоны профиля, аккаунтов и
// конфига, изолируя тесты друг от друга при повторных прогонах (-count=2) и
// в произвольном порядке. Тесты, изолирующие data/ через os.Chdir(t.TempDir()),
// должны вызывать этот хелпер после смены рабочего каталога: тогда синглтоны
// пересоздадутся уже из нового data/{profile.json, accounts, config.json}, а не из
// каталога предыдущего теста (который к тому моменту может быть удалён).
//
// БД этим хелпером намеренно не трогаем: у неё фоновые горутины (автобэкап,
// pHash-backfill), а тест, работающий с БД, сам закрывает её и пересоздаёт
// (см., TestIntegrationReadyMetricsSettingsPassword).

func resetGlobalTestState(t *testing.T) {
	t.Helper()
	profileOnce = sync.Once{}
	profile = nil
	accountsOnce = sync.Once{}
	accountsInst = nil
	config = nil
	configOnce = sync.Once{}
	configCheckAt.Store(0)
	configGen.Store(0)
}
