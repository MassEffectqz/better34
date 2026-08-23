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

Write-Host "BRIEFLY_HOST=$env:BRIEFLY_HOST  BRIEFLY_ALLOWED_HOSTS=$env:BRIEFLY_ALLOWED_HOSTS" -ForegroundColor Cyan
Write-Host "Телефон: http://${ip}:3000" -ForegroundColor Green
go run .