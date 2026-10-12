param(
    [string]$CatalogUrl = 'http://127.0.0.1:3002',
    [string]$InventoryUrl = 'http://127.0.0.1:3004',
    [string]$OrderUrl = 'http://127.0.0.1:3003',
    [string]$PaymentUrl = 'http://127.0.0.1:3007',
    [string]$JwtSecret = $env:JWT_SECRET,
    [ValidateRange(30, 540)][int]$WaitSeconds = 180,
    [switch]$NoOpenBrowser
)

# Creates uniquely named local demo records and one Stripe Sandbox Checkout.
# Auth/Gateway are simulated with local JWTs and a trusted X-User-ID header.
# This script never calls payment-succeeded/payment-failed itself.
$ErrorActionPreference = 'Stop'
foreach ($address in @($CatalogUrl, $InventoryUrl, $OrderUrl, $PaymentUrl)) {
    $uri = [uri]$address
    if (-not $uri.IsAbsoluteUri -or -not $uri.IsLoopback -or $uri.Scheme -notin @('http', 'https') -or $uri.UserInfo -or $uri.Query -or $uri.Fragment) {
        throw 'Use only loopback service origins for this isolated local demo.'
    }
}
if (-not $JwtSecret -or [Text.Encoding]::UTF8.GetByteCount($JwtSecret) -lt 32) {
    throw 'Set JWT_SECRET to the shared local Catalog/Inventory secret (at least 32 UTF-8 bytes).'
}

Add-Type -AssemblyName System.Net.Http
$handler = [System.Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$http = [System.Net.Http.HttpClient]::new($handler)
$http.Timeout = [TimeSpan]::FromSeconds(25)

function ConvertTo-Base64Url([byte[]]$Bytes) {
    return [Convert]::ToBase64String($Bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function New-LocalToken([string]$Subject, [string]$Role) {
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $header = ConvertTo-Base64Url ([Text.Encoding]::UTF8.GetBytes('{"alg":"HS256","typ":"JWT"}'))
    $claims = @{ sub = $Subject; role = $Role; iss = 'gelatoflow-auth'; aud = 'gelatoflow-api'; iat = $now; exp = $now + 1800 }
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
        try { $response = $http.SendAsync($request).GetAwaiter().GetResult() }
        catch { throw "Cannot complete $Method $Url. Check that the service is running and reachable at this address. If a POST timed out, retry the same request/key rather than creating a different order." }
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
        if ($Result.Status -in @(401, 403)) {
            throw "$Step failed: HTTP $($Result.Status); code=$($Result.Data.code). The local demo JWT must match Catalog/Inventory JWT_SECRET, issuer and audience. Reload JWT_SECRET from catalog-service/.env in this terminal before running the script; Payment's key may differ."
        }
        throw "$Step failed: HTTP $($Result.Status); code=$($Result.Data.code); message=$($Result.Data.message); order_id=$($Result.Data.details.order_id)"
    }
}

function Assert-Balances([int]$Available, [int]$Reserved, [int]$Sold) {
    $result = Send-Request 'GET' "$InventoryUrl/api/v1/inventory/batches/$script:batchId" $managerHeaders
    Assert-Status $result 200 'Read batch'
    if ($result.Data.available_portions -ne $Available -or $result.Data.reserved_portions -ne $Reserved -or $result.Data.sold_portions -ne $Sold) {
        throw "Unexpected balances: available=$($result.Data.available_portions), reserved=$($result.Data.reserved_portions), sold=$($result.Data.sold_portions)"
    }
}

try {
    foreach ($address in @($CatalogUrl, $InventoryUrl, $OrderUrl)) {
        Assert-Status (Send-Request 'GET' "$address/health" @{}) 200 "Health $address"
    }
    Assert-Status (Send-Request 'GET' "$PaymentUrl/actuator/health" @{}) 200 'Payment health'
    $managerHeaders = @{ Authorization = 'Bearer ' + (New-LocalToken ([guid]::NewGuid().ToString()) 'MANAGER') }
    $customerId = [guid]::NewGuid().ToString()
    $customerHeaders = @{ 'X-User-ID' = $customerId; Authorization = 'Bearer ' + (New-LocalToken $customerId 'CUSTOMER') }

    # Validate local Manager authentication before creating any demo records.
    Assert-Status (Send-Request 'GET' "$CatalogUrl/api/v1/flavors?active=false" $managerHeaders) 200 'Catalog Manager authentication'
    Assert-Status (Send-Request 'GET' "$InventoryUrl/api/v1/inventory/batches" $managerHeaders) 200 'Inventory Manager authentication'

    $flavor = Send-Request 'POST' "$CatalogUrl/api/v1/flavors" $managerHeaders @{
        name = 'Stripe Sandbox Demo ' + [guid]::NewGuid().ToString(); description = 'Local Order and Payment integration'
        price = @{ amount_minor = 7500; currency = 'THB' }; allergens = @(); recipe = 'demo'; active = $true
    }
    Assert-Status $flavor 201 'Create flavor'
    $batch = Send-Request 'POST' "$InventoryUrl/api/v1/inventory/batches" $managerHeaders @{
        flavor_id = $flavor.Data.id; production_date = [DateTime]::UtcNow.ToString('yyyy-MM-dd')
        expires_at = [DateTime]::UtcNow.AddHours(2).ToString('o'); initial_portions = 10; unit_cost_minor = 100; currency = 'THB'
    }
    Assert-Status $batch 201 'Create batch'
    $script:batchId = $batch.Data.id
    Write-Output "Created flavor=$($flavor.Data.id), batch=$script:batchId"
    Assert-Balances 10 0 0

    $checkoutHeaders = $customerHeaders.Clone()
    $checkoutHeaders['Idempotency-Key'] = [guid]::NewGuid().ToString()
    $checkoutBody = @{ pickup_at = [DateTime]::UtcNow.AddHours(1).ToString('o'); items = @(@{ flavor_id = $flavor.Data.id; portions = 2 }) }
    for ($attempt = 0; $attempt -lt 3; $attempt++) {
        $created = Send-Request 'POST' "$OrderUrl/api/v1/orders" $checkoutHeaders $checkoutBody
        if ($created.Status -notin @(503, 504) -or $created.Data.code -notin @('DEPENDENCY_TIMEOUT', 'DEPENDENCY_UNAVAILABLE') -or -not $created.Data.details.order_id) { break }
        Write-Output 'Checkout dependency unavailable: retrying the identical body and original key.'
        Start-Sleep -Seconds 2
    }
    Assert-Status $created 201 'Create Order through Payment gRPC and Stripe'
    $orderId = $created.Data.order_id
    $checkoutUri = [uri]$created.Data.checkout_url
    if ($created.Data.status -ne 'PENDING_PAYMENT' -or $created.Data.total_amount_minor -ne 15000 -or $created.Data.currency -ne 'THB' -or $checkoutUri.Scheme -ne 'https' -or $checkoutUri.Host -ne 'checkout.stripe.com' -or $checkoutUri.UserInfo) {
        throw 'Unexpected Order total, state or Stripe Checkout URL.'
    }
    if ($checkoutUri.AbsolutePath -notmatch '(^|/)cs_test_[A-Za-z0-9_]+') {
        throw 'Refusing to open a Checkout URL without a Sandbox cs_test_ Session ID. Check Payment STRIPE_LIVE_MODE=false and its sk_test_ key.'
    }
    Assert-Balances 8 2 0
    Write-Output "PASS: Order $orderId PENDING_PAYMENT; available=8 reserved=2 sold=0."
    Write-Output "Customer UUID for later Order inspection: $customerId"
    Write-Output 'Use a Stripe test card only: 4242 4242 4242 4242, future expiry, any three-digit CVC.'
    if ($NoOpenBrowser) {
        Write-Output 'Open checkout_url from this Order create response in your browser; treat it as sensitive.'
        # Explicitly requested only in this mode; never write the URL into a file.
        Write-Output $created.Data.checkout_url
    } else { Start-Process -FilePath $created.Data.checkout_url }

    $deadline = [DateTimeOffset]::UtcNow.AddSeconds($WaitSeconds)
    $paid = $false
    do {
        $current = Send-Request 'GET' "$OrderUrl/api/v1/orders/$orderId" $customerHeaders
        Assert-Status $current 200 'Read Order after payment'
        if ($current.Data.status -eq 'PAID') { $paid = $true; break }
        if ($current.Data.status -in @('CANCELLED', 'CANCEL_PENDING')) { throw "Order became $($current.Data.status). Inspect Stripe webhook, Order logs and stock expiry before retrying." }
        Start-Sleep -Seconds 2
    } while ([DateTimeOffset]::UtcNow -lt $deadline)
    if (-not $paid) { throw "Order $orderId is not PAID yet. Check the Stripe CLI delivery status, Payment callback config and Order/Inventory logs. Demo records remain for inspection." }
    Assert-Balances 8 0 2
    Write-Output 'PASS: Stripe webhook -> Payment HTTP callback -> Order PAID; available=8 reserved=0 sold=2.'
    $replay = Send-Request 'POST' "$OrderUrl/api/v1/orders" $checkoutHeaders $checkoutBody
    Assert-Status $replay 201 'Paid checkout replay'
    if ($replay.Data.order_id -ne $orderId -or $replay.Data.status -ne 'PAID' -or $replay.Data.checkout_url) { throw 'Paid replay returned a different order or a payable URL.' }
    Assert-Balances 8 0 2
    Write-Output 'PASS: Same-key replay does not reserve/sell twice or return a new payment URL.'
    Write-Output 'Demo records remain. This test does not verify Auth login, Gateway, Fulfillment queue or Notification delivery.'
}
finally { $http.Dispose() }
