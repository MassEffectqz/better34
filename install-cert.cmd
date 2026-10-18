@echo off
setlocal
cd /d "%~dp0"
if not exist "data\tls\cert.pem" (
  echo [!] Файл сертификата data\tls\cert.pem не найден.
  echo Сначала запустите сервер run.cmd, чтобы сгенерировать сертификат.
  pause
  exit /b 1
)
echo Установка сертификата better34-local в доверенные корневые центры пользователя Windows...
certutil -user -addstore Root "data\tls\cert.pem"
if %errorlevel% equ 0 (
  echo.
  echo [OK] Сертификат успешно установлен в хранилище доверенных корневых сертификатов!
  echo Перезапустите браузер Chrome или Edge, чтобы изменения вступили в силу.
) else (
  echo.
  echo [!] Ошибка установки сертификата.
)
pause
