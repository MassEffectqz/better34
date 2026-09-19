@echo off
rem run.cmd — запуск briefly без PowerShell (аналог run.ps1, нативный cmd.exe).
rem Работает двойным кликом или из любого терминала. Останавливается при ошибке.
setlocal EnableDelayedExpansion
cd /d "%~dp0"

rem ── 1. Загрузка .env (KEY=VALUE). Уже заданные переменные имеют приоритет. ──
if exist ".env" (
  for /f "usebackq eol=# tokens=1,* delims==" %%A in (".env") do (
    if not "%%B" == "" if not defined %%A (
      set "val=%%B"
      set "val=!val:"=!"
      set "%%A=!val!"
    )
  )
)

rem ── 2. IP интерфейса с маршрутом по умолчанию: из всех default-маршрутов
rem      берём интерфейс с минимальной метрикой (как Get-NetRoute в run.ps1) ──
set "LAN_IP="
set "BEST_METRIC="
for /f "tokens=1-5" %%A in ('route print -4 ^| findstr /r /c:"^  *0\.0\.0\.0  *0\.0\.0\.0"') do (
  rem Только строки Active Routes (5 колонок); у Persistent Routes метрики нет.
  if not "%%E" == "" (
    if not defined LAN_IP (
      set "LAN_IP=%%D"
      set "BEST_METRIC=%%E"
    ) else if %%E LSS !BEST_METRIC! (
      set "LAN_IP=%%D"
      set "BEST_METRIC=%%E"
    )
  )
)

rem Проверка: адаптер с этим IP не должен быть виртуальным (WSL/vEthernet/Docker).
if defined LAN_IP call :check_adapter

rem ── 2b. Fallback: первый приватный IP не-виртуального адаптера. ──
if not defined LAN_IP (
  set "CUR="
  for /f "usebackq delims=" %%L in (`ipconfig`) do (
    set "line=%%L"
    call :is_header "!line!" hdr
    if defined hdr (
      for /f "tokens=1 delims=:" %%X in ("!line!") do set "CUR=%%X"
    ) else if not "!line:IPv4=!" == "!line!" (
      for /f "tokens=2 delims=:" %%I in ("!line!") do (
        set "cand=%%I"
        set "cand=!cand: =!"
        call :private_ip !cand! ok
        if defined ok if not defined LAN_IP (
          set "an=!CUR!"
          if "!an:WSL=!" == "!an!" if "!an:vEthernet=!" == "!an!" if "!an:Docker=!" == "!an!" set "LAN_IP=!cand!"
        )
      )
    )
  )
)

if not defined LAN_IP (
  echo Cannot detect LAN IP
  endlocal
  exit /b 1
)

rem ── 3. Дефолты (как в run.ps1). ──
if not defined BRIEFLY_HOST set "BRIEFLY_HOST=0.0.0.0"
if not defined BRIEFLY_ALLOWED_HOSTS set "BRIEFLY_ALLOWED_HOSTS=%LAN_IP%"
if not defined BRIEFLY_MEDIA_CACHE_GB set "BRIEFLY_MEDIA_CACHE_GB=20"
rem HTTPS + HTTP/2 (self-signed сертификат в data/tls): первый вход — принять
rem исключение безопасности («Дополнительно» → «Перейти на сайт» на телефоне).
if not defined BRIEFLY_TLS set "BRIEFLY_TLS=1"

set "SCHEME=http"
if "%BRIEFLY_TLS%" == "1" set "SCHEME=https"
if /i "%BRIEFLY_TLS%" == "true" set "SCHEME=https"
if /i "%BRIEFLY_TLS%" == "yes" set "SCHEME=https"

echo BRIEFLY_HOST=%BRIEFLY_HOST%  BRIEFLY_ALLOWED_HOSTS=%BRIEFLY_ALLOWED_HOSTS%  BRIEFLY_TLS=%BRIEFLY_TLS%  BRIEFLY_MEDIA_CACHE_GB=%BRIEFLY_MEDIA_CACHE_GB%
echo Phone: %SCHEME%://%LAN_IP%:3000

rem ── 4. Сервер. Переменные выше наследуются дочерним процессом. ──
rem --print: показать конфигурацию и выйти, не запуская сервер.
if /i "%~1" == "--print" (
  echo [dry-run] go run .
  endlocal
  exit /b 0
)
rem ВАЖНО: без endlocal перед go run — иначе setlocal-переменные (BRIEFLY_*,
rem .env) откатятся и сервер запустится без конфигурации.
go run .
set "GOEXIT=%errorlevel%"
endlocal
exit /b %GOEXIT%

rem ── helpers ────────────────────────────────────────────────────────────────

rem check_adapter: если LAN_IP принадлежит виртуальному адаптеру — сбросить.
:check_adapter
set "CUR="
for /f "usebackq delims=" %%L in (`ipconfig`) do (
  set "line=%%L"
  call :is_header "!line!" hdr
  if defined hdr (
    for /f "tokens=1 delims=:" %%X in ("!line!") do set "CUR=%%X"
  ) else if not "!line:IPv4=!" == "!line!" (
    for /f "tokens=2 delims=:" %%I in ("!line!") do (
      set "cand=%%I"
      set "cand=!cand: =!"
      if /i "!cand!" == "%LAN_IP%" (
        set "an=!CUR!"
        if not "!an:WSL=!" == "!an!" set "LAN_IP="
        if not "!an:vEthernet=!" == "!an!" set "LAN_IP="
        if not "!an:Docker=!" == "!an!" set "LAN_IP="
      )
    )
  )
)
exit /b 0

rem is_header <line> <outvar>: ok=1 если строка — заголовок адаптера в ipconfig.
rem Locale-независимо: заголовок не имеет отступа и заканчивается на ':'.
:is_header
set "%2="
set "h=%~1"
if not defined h exit /b 0
if "!h:~0,1!" == " " exit /b 0
if "!h:~-1!" == ":" set "%2=1"
exit /b 0

rem private_ip <addr> <outvar>: ok=1 если адрес приватный (192.168/10/172.16-31).
:private_ip
set "%2="
set "ip=%1"
if "%ip:~0,8%" == "192.168." set "%2=1" & exit /b 0
if "%ip:~0,3%" == "10." set "%2=1" & exit /b 0
if "%ip:~0,4%" == "172." (
  for /f "tokens=2 delims=." %%O in ("%ip%") do (
    if %%O geq 16 if %%O leq 31 set "%2=1"
  )
)
exit /b 0
