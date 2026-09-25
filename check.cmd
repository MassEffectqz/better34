@echo off
rem Р•РґРёРЅС‹Р№ quality gate: С‚Рµ Р¶Рµ npm-РєРѕРјР°РЅРґС‹ РёСЃРїРѕР»СЊР·СѓСЋС‚СЃСЏ РІ CI.
rem check.cmd вЂ” РїРѕР»РЅР°СЏ РїСЂРѕРІРµСЂРєР° РїСЂРѕРµРєС‚Р° Р±РµР· PowerShell (РЅР°С‚РёРІРЅС‹Р№ cmd.exe).
setlocal
cd /d "%~dp0"

call npm run check || goto :fail

echo.
echo ALL CHECKS PASSED
endlocal
exit /b 0

:fail
echo.
echo CHECKS FAILED
endlocal
exit /b 1
