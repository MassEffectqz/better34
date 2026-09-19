@echo off
chcp 65001 >nul
:: install.bat — удобный установщик проекта briefly (better34) для Windows
:: Автоматически:
::   1. Устанавливает Go и Node.js через winget (если не установлены)
::   2. Загружает Go-зависимости
::   3. Устанавливает npm-зависимости
::   4. Собирает frontend
::   5. Создаёт .env из .env.example
setlocal EnableDelayedExpansion

echo.
echo ==============================================
echo    briefly (better34) — автоматическая установка
echo ==============================================
echo.

:: ── 0. Проверка winget ───────────────────────────
echo [0/6] Проверка winget (для автоустановки Go и Node.js)...
where winget >nul 2>&1
if !errorlevel! neq 0 (
    echo    [!] winget не найден. Установите вручную:
    echo      Go:      https://go.dev/dl/
    echo      Node.js:  https://nodejs.org/
    echo.
) else (
    echo    OK — winget найден, можно автоустановить зависимости.
)
echo.

:: ── 1. Проверка и установка Go ───────────────────
echo [1/6] Проверка Go...
where go >nul 2>&1
if !errorlevel! neq 0 (
    echo    Go не найден. Попытка установить через winget...
    where winget >nul 2>&1
    if !errorlevel! equ 0 (
        echo    Устанавливаю Go через winget...
        winget install --id Golang.Go --silent --accept-package-agreements --accept-source-agreements
        if !errorlevel! neq 0 (
            echo  *** Ошибка установки Go через winget. Скачайте вручную: https://go.dev/dl/ ***
            echo.
            pause
            exit /b 1
        )
        echo    OK — Go установлен.
    ) else (
        echo  *** Go не найден и winget недоступен. ***
        echo      Скачайте Go вручную: https://go.dev/dl/
        echo.
        pause
        exit /b 1
    )
) else (
    echo    OK — Go найден.
)
echo.

:: ── 2. Проверка и установка Node.js ──────────────
echo [2/6] Проверка Node.js и npm...
where node >nul 2>&1
if !errorlevel! neq 0 (
    echo    Node.js не найден. Попытка установить через winget...
    where winget >nul 2>&1
    if !errorlevel! equ 0 (
        echo    Устанавливаю Node.js через winget...
        winget install --id OpenJS.NodeJS --silent --accept-package-agreements --accept-source-agreements
        if !errorlevel! neq 0 (
            echo  *** Ошибка установки Node.js через winget. Скачайте вручную: https://nodejs.org/ ***
            echo.
            pause
            exit /b 1
        )
        echo    OK — Node.js установлен.
    ) else (
        echo  *** Node.js не найден и winget недоступен. ***
        echo      Скачайте Node.js вручную: https://nodejs.org/
        echo.
        pause
        exit /b 1
    )
) else (
    echo    OK — Node.js найден.
)

where npm >nul 2>&1
if !errorlevel! neq 0 (
    echo  *** npm не найден! Убедитесь, что Node.js установлен корректно. ***
    pause
    exit /b 1
)
echo    OK — npm найден.
echo.

:: ── 3. go mod download ───────────────────────────
echo [3/6] Загрузка Go-зависимостей...
cd /d "%~dp0"
go mod download
if !errorlevel! neq 0 (
    echo.
    echo  *** Ошибка при загрузке Go-зависимостей! ***
    echo.
    pause
    exit /b 1
)
echo    OK — Go-зависимости загружены.
echo.

:: ── 4. npm install ───────────────────────────────
echo [4/6] Установка npm-зависимостей...
npm install
if !errorlevel! neq 0 (
    echo.
    echo  *** Ошибка при установке npm-зависимостей! ***
    echo.
    pause
    exit /b 1
)
echo    OK — npm-зависимости установлены.
echo.

:: ── 5. npm run build ─────────────────────────────
echo [5/6] Сборка frontend...
call npm run build >nul 2>&1
if !errorlevel! neq 0 (
    echo    [!] Сборка frontend завершилась с ошибкой — запустите вручную:  npm run build
) else (
    echo    OK — Frontend собран.
)
echo.

:: ── 6. .env ──────────────────────────────────────
echo [6/6] Настройка .env...
if not exist ".env" (
    if exist ".env.example" (
        copy /y ".env.example" ".env" >nul
        echo    OK — Создан .env из .env.example
    ) else (
        echo    [!] .env.example не найден — пропускаем создание .env
    )
) else (
    echo    OK — .env уже существует
)

echo.
echo ==============================================
echo    OK — Установка завершена успешно!
echo ==============================================
echo.
echo Теперь вы можете запустить сервер:
echo   run.cmd
echo.
echo Или вручную:
echo   go run .
echo.
echo Затем откройте в браузере:  http://localhost:3000
echo (или https://^<ваш-IP-в-LAN^>:3000 — для доступа с телефона)
echo.
echo ==============================================
echo.

endlocal
pause


