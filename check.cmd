@echo off
rem check.cmd — полная проверка проекта без PowerShell (нативный cmd.exe).
rem Запуск: двойной клик, или `check.cmd` из любого терминала, или `.\check.cmd` из PS.
rem Останавливается на первом же упавшем шаге; код возврата 0 = всё зелёное.
setlocal
cd /d "%~dp0"

echo === [1/6] JS: eslint ===
call npm run lint || goto :fail

echo === [2/6] JS: typecheck ===
call npm run typecheck || goto :fail

echo === [3/6] JS: node --test ===
call npm test || goto :fail

echo === [4/6] Go: build ===
go build ./... || goto :fail

echo === [5/6] Go: vet ===
go vet ./... || goto :fail

echo === [6/6] Go: tests ===
go test ./... || goto :fail

echo.
echo ALL CHECKS PASSED
endlocal
exit /b 0

:fail
echo.
echo CHECKS FAILED (step above returned errorlevel %errorlevel%)
endlocal
exit /b 1
