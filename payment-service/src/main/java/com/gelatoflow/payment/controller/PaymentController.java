package com.gelatoflow.payment.controller;

import com.gelatoflow.payment.dto.CreatePaymentIntentRequest;
import com.gelatoflow.payment.dto.PaymentIntentResponse;
import com.gelatoflow.payment.dto.WebhookResponse;
import com.gelatoflow.payment.exception.ApiException;
import com.gelatoflow.payment.service.PaymentService;
import com.gelatoflow.payment.service.WebhookService;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.validation.Valid;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import org.springframework.http.*;
import org.springframework.security.core.annotation.AuthenticationPrincipal;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/v1/payments")
public class PaymentController {
  private final PaymentService payments;
  private final WebhookService webhooks;
  private static final int MAX_WEBHOOK_BYTES = 262144;

  public PaymentController(PaymentService payments, WebhookService webhooks) {
    this.payments = payments;
    this.webhooks = webhooks;
  }

  @PostMapping(value = "/create-payment-intent", consumes = MediaType.APPLICATION_JSON_VALUE)
  public ResponseEntity<PaymentIntentResponse> create(
      @Valid @RequestBody CreatePaymentIntentRequest request, @AuthenticationPrincipal Jwt jwt) {
    var intent = payments.create(request.orderId(), jwt.getSubject());
    return ResponseEntity.status(HttpStatus.CREATED)
        .cacheControl(CacheControl.noStore())
        .body(
            new PaymentIntentResponse(
                intent.paymentIntentId(),
                intent.clientSecret(),
                intent.amountMinor(),
                intent.currency()));
  }

  @PostMapping(value = "/webhook", consumes = MediaType.APPLICATION_JSON_VALUE)
  public WebhookResponse webhook(
      HttpServletRequest request,
      @RequestHeader(value = "Stripe-Signature", required = false) String signature)
      throws IOException {
    var body = request.getInputStream().readNBytes(MAX_WEBHOOK_BYTES + 1);
    if (body.length > MAX_WEBHOOK_BYTES)
      throw new ApiException(
          HttpStatus.PAYLOAD_TOO_LARGE, "PAYLOAD_TOO_LARGE", "Webhook exceeds 256 KiB");
    webhooks.receive(new String(body, StandardCharsets.UTF_8), signature);
    return new WebhookResponse(true);
  }
}
