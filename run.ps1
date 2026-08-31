$ErrorActionPreference = "Stop"

# IP интерфейса, через который идёт маршрут в интернет (не WSL/VPN/Docker).
$ip = $null
try {
    $rt = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction Stop |
        Where-Object { $_.InterfaceAlias -notmatch 'Loopback|vEthernet|WSL|Docker' } |
        Sort-Object RouteMetric | Select-Object -First 1
    if ($rt) {
        $ip = (Get-NetIPAddress -InterfaceIndex $rt.ifIndex -AddressFamily IPv4 -ErrorAction Stop |
            Where-Object { $_.IPAddress -notlike '169.254.*' } |
            Select-Object -First 1).IPAddress
    }
} catch {}

# Fallback: старая эвристика по первому приватному адресу.
if (-not $ip) {
    $ip = (Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -match '^192\.168\.|^10\.|^172\.(1[6-9]|2\d|3[01])\.' } |
        Select-Object -First 1).IPAddress
}

if (-not $ip) {
    Write-Host "Не удалось определить локальный IP" -ForegroundColor Yellow
    exit 1
}

# Сервер с версии 0.x сам добавляет все свои сетевые IP в белый список,
# переменные нужны только для нестандартных сценариев.
if (-not $env:BRIEFLY_HOST) {
    $env:BRIEFLY_HOST = "0.0.0.0"
}
if (-not $env:BRIEFLY_ALLOWED_HOSTS) {
    $env:BRIEFLY_ALLOWED_HOSTS = $ip
}
# Бюджет дискового media-cache (общий для всех устройств в LAN): чем больше,
# тем чаще телефон получает контент с диска ПК, а не с CDN. Гигабайты.
if (-not $env:BRIEFLY_MEDIA_CACHE_GB) {
    $env:BRIEFLY_MEDIA_CACHE_GB = "20"
}
# HTTPS + HTTP/2 (self-signed сертификат в data/tls): убирает лимит в 6
# параллельных соединений браузера — лента миниатюр грузится пачками.
# Первый вход: браузер предупредит о самоподписанном сертификате, примите
# исключение (на телефоне: «Дополнительно» → «Перейти на сайт»).
if (-not $env:BRIEFLY_TLS) {
    $env:BRIEFLY_TLS = "1"
}

$scheme = if ($env:BRIEFLY_TLS -in @("1", "true", "yes")) { "https" } else { "http" }

Write-Host "BRIEFLY_HOST=$env:BRIEFLY_HOST  BRIEFLY_ALLOWED_HOSTS=$env:BRIEFLY_ALLOWED_HOSTS  BRIEFLY_TLS=$env:BRIEFLY_TLS  BRIEFLY_MEDIA_CACHE_GB=$env:BRIEFLY_MEDIA_CACHE_GB" -ForegroundColor Cyan
Write-Host "Телефон: ${scheme}://${ip}:3000" -ForegroundColor Green
go run .