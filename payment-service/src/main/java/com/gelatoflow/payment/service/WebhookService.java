package com.gelatoflow.payment.service;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.gelatoflow.contracts.order.v1.*;
import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.gelatoflow.payment.config.PaymentProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.stripe.exception.SignatureVerificationException;
import com.stripe.model.Event;
import com.stripe.net.Webhook;
import java.time.Instant;
import java.util.Locale;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Service;

@Service
public class WebhookService {
  private final PaymentProperties properties;
  private final ObjectMapper mapper;
  private final StripePort stripe;
  private final OrderPort orders;
  private final PaymentService payments;

  public WebhookService(
      PaymentProperties properties,
      ObjectMapper mapper,
      StripePort stripe,
      OrderPort orders,
      PaymentService payments) {
    this.properties = properties;
    this.mapper = mapper;
    this.stripe = stripe;
    this.orders = orders;
    this.payments = payments;
  }

  public void receive(String payload, String signature) {
    Event event;
    try {
      if (signature == null || signature.isBlank()) throw invalidWebhook();
      // Verify the unmodified body before parsing or performing any side effects.
      Webhook.Signature.verifyHeader(payload, signature, properties.stripe().webhookSecret(), 300);
      event = Webhook.constructEventWithoutVerification(payload);
    } catch (SignatureVerificationException
        | com.google.gson.JsonParseException
        | IllegalArgumentException e) {
      throw invalidWebhook();
    }
    if (event == null
        || event.getId() == null
        || !event.getId().matches("evt_[A-Za-z0-9]+")
        || event.getCreated() == null
        || !Boolean.valueOf(properties.stripe().liveMode()).equals(event.getLivemode()))
      throw invalidWebhook();
    var type = event.getType();
    if (!"payment_intent.succeeded".equals(type) && !"payment_intent.payment_failed".equals(type))
      return;

    String intentId;
    try {
      var object = mapper.readTree(payload).path("data").path("object");
      intentId = object.path("id").asText();
      if (!"payment_intent".equals(object.path("object").asText())
          || !intentId.matches("pi_[A-Za-z0-9]+")) throw invalidWebhook();
    } catch (java.io.IOException e) {
      throw invalidWebhook();
    }

    // Retrieve current provider state instead of trusting a stale failure snapshot.
    // This also avoids event-deserializer failures when webhook API versions differ.
    var intent = stripe.retrieve(intentId);
    payments.validateIntent(intent);
    if (!intentId.equals(intent.getId())) throw invalidWebhook();
    var outcome =
        "payment_intent.succeeded".equals(type)
            ? PaymentOutcome.PAYMENT_OUTCOME_SUCCEEDED
            : PaymentOutcome.PAYMENT_OUTCOME_FAILED;
    if (outcome == PaymentOutcome.PAYMENT_OUTCOME_SUCCEEDED) {
      if (!"succeeded".equals(intent.getStatus())
          || !intent.getAmount().equals(intent.getAmountReceived())) {
        throw new ApiException(
            HttpStatus.SERVICE_UNAVAILABLE,
            "PAYMENT_NOT_RECONCILED",
            "Successful payment could not be reconciled; retry delivery");
      }
    } else if (!"requires_payment_method".equals(intent.getStatus())) {
      return; // Failure event was superseded by a later attempt; success arrives separately.
    }
    orders.report(
        ReportPaymentResultRequest.newBuilder()
            .setOrderId(intent.getMetadata().get("order_id"))
            .setPaymentIntentId(intentId)
            .setProviderEventId(event.getId())
            .setOutcome(outcome)
            .setAmountMinor(intent.getAmount())
            .setCurrency(intent.getCurrency().toUpperCase(Locale.ROOT))
            .setOccurredAt(Instant.ofEpochSecond(event.getCreated()).toString())
            .build());
  }

  private ApiException invalidWebhook() {
    return new ApiException(
        HttpStatus.BAD_REQUEST, "INVALID_WEBHOOK", "Invalid Stripe webhook payload or signature");
  }
}
