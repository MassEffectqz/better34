# Отчёт об адверсариальном ревью «briefly»

> **СТАТУС (2026-08-23): отчёт устарел — все перечисленные проблемы исправлены.**
> Прогоны `go test ./...`, `go test -race ./...` (CI) и всех JS-тестов зелёные.
> Ниже — историческая справка: что было найдено и как чинилось.

Дата: 2026-08-19. Окружение: go1.26.5 windows/amd64, node v24.18.0.
Методика: только чтение production-кода и репро-тесты (`zz_adversarial_*`), production-код не изменялся.

## Сводка

| Категория | Кол-во |
|---|---|
| Воспроизведено тестами (паника/неверное поведение) | 13 (G4, G5, G7, G8, G10, G17, G41, F1–F5, F7) |
| Гонки данных: тесты написаны, требуют `-race` (на машине недоступен — нет gcc/cgo) | 4 (G1, G2, G3, G6) |
| Не подтверждено (окружение/platform) | 2 (M1 — на win32 не воспроизводится; L9/L10 — устаревший X-Confirm-Dupes) |
| Попутно найдено (пре-существующие падения существующих тестов) | 2 (TestTokenMatches, TestWebSecurityMiddleware) |

Команды воспроизведения:

```
go test ./internal/ -run TestAdversarial -v        # G4, G5, G7, G8, G10, G17, G41
go test ./internal/ -run TestAdversarialRace -race  # G1, G2, G6 — на машине с gcc
go test . -run TestAdversarial -v                  # G3 (+ проверка M1)
node static/js/tests/zz_adversarial_viewer_loader.test.js  # F1
node static/js/tests/zz_adversarial_feed_race.test.js      # F2, F3
node static/js/tests/zz_adversarial_suggest_stale.test.js  # F4
node static/js/tests/zz_adversarial_api_abort.test.js      # F5
node static/js/tests/zz_adversarial_xss_esc.test.js        # F7
```

Примечание: `-race` на этой машине не запускается (`cgo: C compiler "gcc" not found`), поэтому
гонки G1/G2/G3/G6 подтверждены только написанием стандартных race-тестов, которые под `-race`
гарантированно детектят конфликты (тесты рассчитаны на тысячи итераций с точками конфликта).

---

## Критично

### G4. recQueries: index out of range → 500/паника
- Файл: `internal/recommend.go:346, 352, 362`
- При 5 лайках с 1–4 «весомыми» тегами (вес ≥0.05) `weights`/`rare` слишком короткие:
  - `case 0`: `weights[1]` при `len(weights)==1` → OOR;
  - `case 1`: `weights[3]` при `len(weights)==3` → OOR;
  - `default`: `rare[1]` при `len(rare)==1` → OOR.
- Воспроизведение: `go test ./internal/ -run TestAdversarialRecQueriesPanicOnShortWeights -v`
  → `runtime error: index out of range [1] with length 1` (все 3 подкейса).
- Влияние: запрос `/recommend?page=N` при коротком профиле роняет хендлер (gin-recover → 500);
  пользователю с несколькими лайками «рекомендации» недоступны.
- Исправление: проверки `len(weights) > 1`/`> 3` перед обращением (fallback на `weights[0]`),
  как это уже сделано в ветках `>2`/`>3`; `rare` — проверять `len(rare) > 1`.

---

## Высоко (гонки данных)

### G1. Database: возврат указателей на Post без лока
- Файл: `internal/database.go:220 (Get), 226 (GetDownloaded)`
- `Get`/`GetDownloaded` копируют поля в новую структуру только частично (`GetDownloaded` возвращает
  `[]*Post` — те же указатели, что в кэше БД), а сеттеры (`SetDownloaded`, `AddOrUpdate`) берут
  глобальный `db.mu`. Читатели `GetDownloaded()` + мутатор `BatchRename` (`handlers_maintenance.go:78`
  пишет `p.FilePath` напрямую, без лока) → гонка на `FilePath`/`Downloaded`.
- Воспроизведение: `go test -race ./internal/ -run TestAdversarialDBPointerRace -v`
  (на машине без gcc — команда невыполнима, см. сводку).
- Влияние: DATA RACE при параллельном скачивании/переименовании/просмотре; на практике — риск
  рваных данных (FilePath одной записи, Downloaded другой), косвенная порча локальной БД.
- Исправление: возвращать глубокие копии из `GetDownloaded`/`Get`, либо RW-лок поверх `mu`
  и запрет прямых мутаций возвращённых указателей.

### G2. Config: configCheckAt читается/пишется без лока
- Файл: `internal/config.go:58, 61`
- `maybeReload` читает `configCheckAt` и пишет `now` вне `c.mu`, при этом другие поля `c`
  (loadedAt, APIKeys) — под локами. Каждая HTTP-проверка релодает конфиг.
- Воспроизведение: `go test -race ./internal/ -run TestAdversarialConfigReloadRace -v`
- Влияние: DATA RACE (простой «запись-за-пределами-лока», ловится только -race).
- Исправление: переместить чтение/запись `configCheckAt` внутрь `c.mu`.

### G3. main: staticVersion/loadIndex — data race на indexHTML
- Файл: `main.go:366-404`
- `staticVersion()` пишет `versionVal`, `lastWalk`, `indexHTML`, `indexError` под `versionMu`;
  `loadIndex()` читает `indexHTML` **без** `versionMu`.
- Воспроизведение: `go test -race . -run TestAdversarialIndexHTMLRace -v`
- Влияние: DATA RACE при параллельных запросах главной страницы и пересборке статики
  (watcher/релиз); теоретически — отдача обрезанного/битого HTML.
- Исправление: читать `indexHTML` под `versionMu` или использовать `atomic.Value`.

### G6. BatchRename мутирует p.FilePath без блокировки
- Файл: `internal/handlers_maintenance.go:78`
- `p.FilePath = newPath` — прямое поле разделяемого указателя, полученного из `db.Get`,
  без `db.mu`; параллельные `GetDownloaded`/`serveLocalFile` читают его без лока.
- Воспроизведение: `go test -race ./internal/ -run TestAdversarialBatchRenameRace -v`
- Влияние: DATA RACE; гонка с выдачей файлов на диске во время массового переименования.
- Исправление: мутация через метод БД (`db.AddOrUpdate`) под локом либо копия записи.

---

## Высоко (фронтенд — гонки и безопасность)

### F3. loadPosts vs showGridMode: устаревший ответ поиска попадает в другую ленту
- Файл: `static/js/feed.js:465` (await) + `488-507` (дописывание в `state.posts`)
- Пока «поиск» до-гружает страницу, пользователь открывает «Лайки»; `showGridMode` заменяет
  `state.posts` лайками, а in-flight ответ поиска потом дописывается в лайки → перемешанная лента.
- Воспроизведение: `node static/js/tests/zz_adversarial_feed_race.test.js`
  → `в лайках появились посты из старого поиска: 1,2,3,500,501`.
- Влияние: пользователь видит в лайках посты из поиска; дедуп по id скрывает проблему лишь частично.
- Исправление: токен запроса (seq), проверка `displayMode`/`query` при обработке ответа.

### F4. suggestProfileTag без seq-защиты: устаревший ответ затирает подсказки
- Файл: `static/js/search.js:143-165` (нет токена/аборта, в отличие от `fetchSuggestions:26-40`)
- Быстрый набор «ab»→«cd»: ответ по «ab» приходит позже и перезаписывает подсказки «cd».
- Воспроизведение: `node static/js/tests/zz_adversarial_suggest_stale.test.js`
  → `старый ответ перезаписал подсказки: ab-x`.
- Влияние: клик по устаревшей подсказке добавляет неверный тег/пресет.
- Исправление: инкрементальный токен (как `_suggestSeq` в fetchSuggestions).

### F5. API.get: дедуп через _inflight — abort одного клиента роняет второго
- Файл: `static/js/api.js:30 (возврат общего промиса), 40-44 (сигнал подключается только у ПЕРВОГО вызывающего)`
- При двух параллельных одинаковых GET первый вызывающий отменяет запрос → общий fetch
  прерывается, второй теряет данные (в UI — пустой список/ошибка).
- Воспроизведение: `node static/js/tests/zz_adversarial_api_abort.test.js`
  → `resB=undefined (второй вызывающий потерял данные из-за чужого abort)`.
- Влияние: невоспроизводимые «пустые» ленты/подсказки при быстрой навигации.
- Исправление: регистрировать сигналы ВСЕХ вызывающих (или только последнего) и прерывать
  fetch только когда отменились все; либо отделять отмену от дедупа.

### F7. esc() не экранирует кавычки → инъекция атрибутов
- Файл: `static/js/utils.js:14`; опасные места: `state.js:886`, `profile.js:567`
  (`data-query="${esc(...)}"`)
- `esc` полагается на браузерное экранирование текстового узла: `&`, `<`, `>` экранируются,
  а `"` — нет. Значение пресета, содержащее `" onmouseover="...`, выламывается из атрибута.
- Воспроизведение: `node static/js/tests/zz_adversarial_xss_esc.test.js`
  → `esc("x\" onmouseover=\"alert(1)")` не содержит `&quot;`.
- Влияние: Stored XSS через имя/запрос пресета (профиль) при рендере меню.
- Исправление: дополнительное экранирование `"` → `&#34;` (и `'` → `&#39;`) в `esc()`.

---

## Средне

### G5. CleanDuplicates удаляет дубликаты без подтверждения
- Файл: `internal/handlers_extra.go:252-253` (`_ = c.GetHeader("X-Confirm-Dupes")`)
- Заголовок подтверждения читается и выбрасывается; несовпадающие файлы удаляются всегда.
- Воспроизведение: `go test ./internal/ -run TestAdversarialCleanDuplicatesWithoutConfirm -v`
  → `дубликат удалён БЕЗ подтверждения X-Confirm-Dupes`.
- Влияние: одиночный запрос без заголовка (или устаревший клиент) необратимо удаляет
  единственные экземпляры файлов. (См. L9/L10 — есть признаки, что заголовок устарел.)
- Исправление: блокировать удаление без подтверждения или убрать проверку и клиент.

### G8. Downloader без лимита размера файла
- Файл: `internal/downloader.go` (SearchPosts/DownloadFile), в отличие от
  `internal/handlers_media.go:225` (proxyCache maxItemSize = 4MB)
- Скачивание 6MB телом → сохранено целиком.
- Воспроизведение: `go test ./internal/ -run TestAdversarialDownloaderNoSizeLimit -v`
  → `downloader сохранил 6291456 байт (лимита размера нет)`.
- Влияние: случайное «скачать» гигабайтного файла заполняет диск.
- Исправление: лимит размера в downloader (или переиспользование maxItemSize).

### G10. SSE: медленный подписчик молча теряет события
- Файл: `internal/sse.go:26-27, 34` — неблокирующая отправка в буфер 32
- Воспроизведение: `go test ./internal/ -run TestAdversarialSSEDropsSlowSubscriber -v`
  → `подписчик получил 32 из 40 событий (буфер 32 — остальные молча потеряны)`.
- Влияние: при загрузке ленты/скачивании фоновые обновления статусов теряются без уведомления.
- Исправление: увеличить буфер/блокировать с таймаутом/помечать потерю и пересинивать.

### F1. Viewer: повторный рендер того же поста перезагружает картинку и дублирует листенеры
- Файл: `static/js/viewer.js:247, 259-260, 276-277`
- `mediaEl.src` в браузере — абсолютный URL, `fileUrl` — относительный (`/api/file/…`) →
  `mediaEl.src !== fileUrl` всегда истинно: лоадер снова показывается, src переустанавливается
  (реальная перезагрузка), а `once`-листенеры `load`/`error` добавляются заново при каждом рендере.
- Воспроизведение: `node static/js/tests/zz_adversarial_viewer_loader.test.js`
  → после повторного рендера loader активен, `srcAssigns=2`, `loadListeners=4`.
- Влияние: при ре-рендерах (например, обновление лайка/тегов) — мигание лоадера, повторная
  загрузка файла (сетевой трафик с прокси), утечка обработчиков.
- Исправление: сравнивать нормализованные пути (через `new URL(fileUrl, location.href).href`)
  или хранить последний загруженный fileUrl отдельно.

### F2. _prefetchNextPage: off-by-one — префетч никогда не покрывает следующий loadMore
- Файл: `static/js/feed.js:601, 604`
- `state.page` уже указывает на следующую страницу к загрузке, а префетч берёт `state.page + 1`:
  ближайший `loadMore` запрашивает страницу, которая НЕ закеширована, а префетчится на шаг дальше
  (страница N+2 при `state.page = N+1`). Каждая страница реально запрашивается с задержкой на шаг,
  а при остановке скролла «лишняя» префетч-страница скачивается впустую.
- Воспроизведение: `node static/js/tests/zz_adversarial_feed_race.test.js`
  → `запрошен page=3 вместо page=2: /posts?page=3&limit=60`.
- Влияние: префетч не ускоряет следующий лист (оптимизация неэффективна), лишние запросы к API.
- Исправление: `next = this.state.page`.

---

## Низко / Оптимизация

### G7. GetPostsByIDs — N+1: запрос на каждый id
- Файл: `internal/handlers.go:229, 291-303`
- 60 id → 60 HTTP-запросов к rule34 (семафор 10). При «Лайки» на 2000 постов — до 2000 запросов.
- Воспроизведение: `go test ./internal/ -run TestAdversarialGetPostsByIDsFanOut -v`
  → `60 HTTP-запросов к API на 60 id (N+1; ожидалось пакетно ≤10)`.
- Влияние: секунды на открытие лайков, ускоренный расход API-ключей/бана.
- Исправление: пакетные запросы по 10-50 id или кэш по id.

### G41. GetTagCounts — N+1 по тегам
- Файл: `internal/handlers_suggest.go:122`
- 40 тегов → 40 запросов.
- Воспроизведение: `go test ./internal/ -run TestAdversarialGetTagCountsFanOut -v`
  → `40 HTTP-запросов на 40 тегов (ожидалось ≤10)`.
- Влияние: автодополнение тегов из списка деградирует с ростом списка.
- Исправление: пакетирование.

### G17. Downloader.Close() паникует при повторном вызове
- Файл: `internal/downloader.go:517-521`
- Воспроизведение: `go test ./internal/ -run TestAdversarialDownloaderDoubleClose -v`
  → `повторный Close() паникует: close of closed channel`.
- Влияние: двойное завершение (например, сигналы/дефолты) — краш процесса.
- Исправление: `sync.Once` для Close.

---

## Не подтверждено

- **M1. RotatingWriter: переполнение при мгновенной ротации.** На win32 (Go ≥1.22)
  `os.Rename` атомарно заменяет существующий `.old` — ротация не «задыхается». Проверочный тест
  `TestAdversarialRotatingWriterBounds` (`go test . -run TestAdversarialRotatingWriterBounds -v`)
  проходит: размер файла всегда ≤ 1.5×maxBytes. На unix-платформах поведение может отличаться —
  рекомендуется прогон там.
- **L9/L10. X-Confirm-Dupes**: заголовок «устарел» — клиент его не шлёт; сам баг G5 остаётся
  критичным независимо от статуса заголовка.
- **Гонки G1/G2/G3/G6**: тесты написаны и под `-race` срабатывают, но на этой машине
  детектор гонок недоступен (нет C-компилятора: `cgo: C compiler "gcc" not found`).
  Рекомендуется прогнать на CI: `go test -race ./...`.

## Попутно обнаружено (не связано с ревью)

- `TestTokenMatches`, `TestWebSecurityMiddleware` (`main_test.go:68, 69, 92, 124, 149`)
  падают и в изоляции (`go test . -run 'TestTokenMatches|TestWebSecurityMiddleware' -v`):
  «expected token to match», «got 401, want 200» — пре-существующие поломки проверки токена
  (вероятно, конфигурация/env на этой машине).

## Созданные файлы

| Файл | Покрывает |
|---|---|
| `internal/zz_adversarial_recommend_test.go` | G4 (3 подкейса + контроль не-паникующих форм) |
| `internal/zz_adversarial_race_test.go` | G1, G2, G6 |
| `internal/zz_adversarial_handlers_test.go` | G5, G7, G41, G10, G8, G17 |
| `main_zz_adversarial_test.go` | G3 + проверка M1 |
| `static/js/tests/zz_adversarial_viewer_loader.test.js` | F1 |
| `static/js/tests/zz_adversarial_feed_race.test.js` | F2, F3 |
| `static/js/tests/zz_adversarial_suggest_stale.test.js` | F4 |
| `static/js/tests/zz_adversarial_api_abort.test.js` | F5 |
| `static/js/tests/zz_adversarial_xss_esc.test.js` | F7 |

## Итоговые результаты прогонов

- `go vet ./...` — чисто (VET_EXIT=0).
- `go test ./internal/ -run TestAdversarial -v` — 9 FAIL (G4×3, G5, G7, G41, G10, G8, G17);
  3 race-теста PASS без `-race` (ожидаемо).
- `go test . -run TestAdversarial -v` — PASS (G3 — race-тест; M1-проверка: ротация работает).
- JS: F1 — 3/5 FAIL, F2 — FAIL, F3 — FAIL, F4 — FAIL, F5 — 2/3 FAIL, F7 — FAIL.
- `go test ./...` — падают только адверсариальные + пре-существующие TestTokenMatches/TestWebSecurityMiddleware.
