param(
    [string]$CatalogUrl = 'http://127.0.0.1:3002',
    [string]$InventoryUrl = 'http://127.0.0.1:3004',
    [string]$OrderUrl = 'http://127.0.0.1:3003',
    [string]$JwtSecret = $env:JWT_SECRET,
    [string]$PaymentServiceToken = $env:PAYMENT_SERVICE_TOKEN
)

# Local smoke test: real Catalog/Inventory/Order, Payment intentionally offline.
# JWTs and trusted Gateway headers are simulated, not an Auth/Gateway integration.
$ErrorActionPreference = 'Stop'
foreach ($address in @($CatalogUrl, $InventoryUrl, $OrderUrl)) {
    if (-not ([uri]$address).IsLoopback) { throw 'This smoke test only supports localhost services.' }
}
if ([string]::IsNullOrEmpty($JwtSecret) -or $JwtSecret.Length -lt 32) {
    throw 'Set JWT_SECRET to the shared local Catalog/Inventory signing secret (at least 32 bytes).'
}
if ([string]::IsNullOrEmpty($PaymentServiceToken)) {
    throw 'Set PAYMENT_SERVICE_TOKEN to the local Order callback token.'
}

Add-Type -AssemblyName System.Net.Http
$http = [System.Net.Http.HttpClient]::new()
$http.Timeout = [TimeSpan]::FromSeconds(20)

function ConvertTo-Base64Url([byte[]]$Bytes) {
    return [Convert]::ToBase64String($Bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function New-LocalToken([string]$Subject, [string]$Role) {
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $header = ConvertTo-Base64Url ([Text.Encoding]::UTF8.GetBytes('{"alg":"HS256","typ":"JWT"}'))
    $claims = @{ sub = $Subject; role = $Role; iss = 'gelatoflow-auth'; aud = 'gelatoflow-api'; iat = $now; exp = $now + 3600 }
    $payload = ConvertTo-Base64Url ([Text.Encoding]::UTF8.GetBytes(($claims | ConvertTo-Json -Compress)))
    $unsigned = "$header.$payload"
    $hmac = [Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes($JwtSecret))
    try { $signature = ConvertTo-Base64Url ($hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes($unsigned))) }
    finally { $hmac.Dispose() }
    return "$unsigned.$signature"
}

function Send-Request([string]$Method, [string]$Url, [hashtable]$Headers, $Body = $null) {
    $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::new($Method), $Url)
    try {
        foreach ($name in $Headers.Keys) { [void]$request.Headers.TryAddWithoutValidation($name, [string]$Headers[$name]) }
        if ($null -ne $Body) {
            $request.Content = [System.Net.Http.StringContent]::new(($Body | ConvertTo-Json -Depth 10 -Compress), [Text.Encoding]::UTF8, 'application/json')
        }
        $response = $http.SendAsync($request).GetAwaiter().GetResult()
        try {
            $raw = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            $data = $null
            if ($raw.StartsWith('{') -or $raw.StartsWith('[')) { $data = $raw | ConvertFrom-Json }
            return @{ Status = [int]$response.StatusCode; Data = $data }
        }
        finally { $response.Dispose() }
    }
    finally { $request.Dispose() }
}

function Assert-Status($Result, [int]$Expected, [string]$Step) {
    if ($Result.Status -ne $Expected) {
        throw "$Step failed: HTTP $($Result.Status); code=$($Result.Data.code); message=$($Result.Data.message)"
    }
}

function Assert-Balances([int]$Available, [int]$Reserved, [int]$Sold) {
    $result = Send-Request 'GET' "$InventoryUrl/api/v1/inventory/batches/$script:batchId" $managerHeaders
    Assert-Status $result 200 'Read batch'
    $batch = $result.Data
    if ($batch.available_portions -ne $Available -or $batch.reserved_portions -ne $Reserved -or $batch.sold_portions -ne $Sold) {
        throw "Unexpected balances: available=$($batch.available_portions), reserved=$($batch.reserved_portions), sold=$($batch.sold_portions)"
    }
}

function New-PendingOrder {
    $headers = @{ 'X-User-ID' = $customerId; Authorization = "Bearer $customerToken"; 'Idempotency-Key' = [guid]::NewGuid().ToString() }
    $body = @{ pickup_at = [DateTime]::UtcNow.AddHours(1).ToString('o'); items = @(@{ flavor_id = $script:flavorId; portions = 2 }) }
    $result = Send-Request 'POST' "$OrderUrl/api/v1/orders" $headers $body
    if ($result.Status -notin @(503, 504) -or $result.Data.code -notin @('DEPENDENCY_UNAVAILABLE', 'DEPENDENCY_TIMEOUT') -or -not $result.Data.details.order_id) {
        throw "Expected offline Payment after a successful reservation; got HTTP $($result.Status), code=$($result.Data.code). Stop Payment for this test."
    }
    return @{ Id = $result.Data.details.order_id; Headers = $headers; Body = $body }
}

function Read-Order([string]$Id) {
    $result = Send-Request 'GET' "$OrderUrl/api/v1/orders/$Id" @{ 'X-User-ID' = $customerId }
    Assert-Status $result 200 'Read order'
    return $result.Data
}

try {
    foreach ($address in @($CatalogUrl, $InventoryUrl, $OrderUrl)) {
        Assert-Status (Send-Request 'GET' "$address/health" @{}) 200 "Health $address"
    }
    $managerHeaders = @{ Authorization = 'Bearer ' + (New-LocalToken ([guid]::NewGuid().ToString()) 'MANAGER') }
    $customerId = [guid]::NewGuid().ToString()
    $customerToken = New-LocalToken $customerId 'CUSTOMER'

    $result = Send-Request 'POST' "$CatalogUrl/api/v1/flavors" $managerHeaders @{
        name = 'Order Smoke ' + [guid]::NewGuid().ToString(); description = 'Local integration test'
        price = @{ amount_minor = 7500; currency = 'THB' }; allergens = @(); recipe = 'test'; active = $true
    }
    Assert-Status $result 201 'Create flavor'
    $script:flavorId = $result.Data.id
    $result = Send-Request 'POST' "$InventoryUrl/api/v1/inventory/batches" $managerHeaders @{
        flavor_id = $script:flavorId; production_date = [DateTime]::UtcNow.ToString('yyyy-MM-dd')
        expires_at = [DateTime]::UtcNow.AddHours(2).ToString('o'); initial_portions = 10; unit_cost_minor = 100; currency = 'THB'
    }
    Assert-Status $result 201 'Create batch through Catalog gRPC validation'
    $script:batchId = $result.Data.id
    Assert-Balances 10 0 0
    Write-Output 'PASS: Inventory -> Catalog gRPC validation'

    $pending = New-PendingOrder
    $order = Read-Order $pending.Id
    if ($order.status -ne 'PENDING_PAYMENT' -or $order.total_amount_minor -ne 15000) { throw 'Incorrect order state or Catalog price snapshot.' }
    Assert-Balances 8 2 0
    $replay = Send-Request 'POST' "$OrderUrl/api/v1/orders" $pending.Headers $pending.Body
    if ($replay.Status -notin @(503, 504) -or $replay.Data.details.order_id -ne $pending.Id) { throw 'Order replay did not reuse the pending order.' }
    Assert-Balances 8 2 0
    Write-Output 'PASS: Order -> Catalog pricing, Order -> Inventory reserve and idempotent replay'

    Assert-Status (Send-Request 'POST' "$OrderUrl/api/v1/orders/$($pending.Id)/cancel" @{ 'X-User-ID' = $customerId } @{ reason = 'CUSTOMER_REQUEST' }) 204 'Cancel order'
    if ((Read-Order $pending.Id).status -ne 'CANCELLED') { throw 'Cancellation did not finalize.' }
    Assert-Balances 10 0 0
    $replay = Send-Request 'POST' "$OrderUrl/api/v1/orders" $pending.Headers $pending.Body
    Assert-Status $replay 201 'Cancelled checkout replay'
    if ($replay.Data.status -ne 'CANCELLED' -or $replay.Data.checkout_url) { throw 'Cancelled checkout replay exposed a payment URL.' }
    Write-Output 'PASS: Cancellation -> Inventory release; replay has no checkout URL'

    $pending = New-PendingOrder
    $callback = "$OrderUrl/api/v1/orders/$($pending.Id)/payment-succeeded"
    Assert-Status (Send-Request 'POST' $callback @{}) 401 'Unauthenticated payment callback'
    $paymentHeaders = @{ 'X-Payment-Service-Token' = $PaymentServiceToken }
    Assert-Status (Send-Request 'POST' $callback $paymentHeaders) 204 'Simulated verified payment callback'
    Assert-Status (Send-Request 'POST' $callback $paymentHeaders) 204 'Payment callback replay'
    if ((Read-Order $pending.Id).status -ne 'PAID') { throw 'Payment callback did not finalize.' }
    Assert-Balances 8 0 2
    Assert-Status (Send-Request 'POST' "$OrderUrl/api/v1/orders/$($pending.Id)/cancel" @{ 'X-User-ID' = $customerId } @{ reason = 'CUSTOMER_REQUEST' }) 409 'Cancel paid order'
    Write-Output 'PASS: Simulated Payment -> Order callback, Order -> Inventory confirmation and replay'
    Write-Output "Test flavor=$script:flavorId batch=$script:batchId. Test records remain for inspection."
    Write-Output 'This does not verify Payment gRPC success, Stripe, Auth login, Gateway or RabbitMQ consumer processing.'
}
finally { $http.Dispose() }
