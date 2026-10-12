package com.gelatoflow.payment.service;

import com.gelatoflow.payment.client.order.OrderCallbackPort;
import com.gelatoflow.payment.client.stripe.CheckoutWebhookPort;
import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.config.PaymentProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.gelatoflow.payment.validation.Ids;
import com.stripe.model.PaymentIntent;
import com.stripe.model.checkout.Session;
import java.time.Instant;
import java.util.Currency;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Service;

@Service
public class CheckoutWebhookService {
  private static final Set<String> EVENTS =
      Set.of(
          "checkout.session.completed",
          "checkout.session.async_payment_succeeded",
          "checkout.session.async_payment_failed",
          "checkout.session.expired");
  private final CheckoutWebhookPort stripe;
  private final OrderCallbackPort orders;
  private final boolean enabled;
  private final boolean liveMode;

  public CheckoutWebhookService(
      CheckoutWebhookPort stripe,
      OrderCallbackPort orders,
      OrderCallbackProperties callbacks,
      PaymentProperties properties) {
    this.stripe = stripe;
    this.orders = orders;
    enabled = callbacks.enabled();
    liveMode = properties.stripe().liveMode();
  }

  public boolean supports(String type) {
    return EVENTS.contains(type);
  }

  public void receive(String type, String sessionId) {
    if (!enabled) throw unreconciled();
    var session = stripe.retrieve(sessionId);
    validateSession(sessionId, session);
    var intent = session.getPaymentIntentObject();
    if (session.getPaymentIntent() != null && intent == null) throw unreconciled();
    if (intent != null) validateIntent(session, intent);

    boolean success =
        "checkout.session.completed".equals(type)
            || "checkout.session.async_payment_succeeded".equals(type);
    if ("paid".equals(session.getPaymentStatus())) {
      if (!"complete".equals(session.getStatus())
          || intent == null
          || !"succeeded".equals(intent.getStatus())
          || !session.getAmountTotal().equals(intent.getAmountReceived())) throw unreconciled();
      // Ignore an old failure once the provider confirms payment succeeded.
      if (success) orders.succeeded(session.getMetadata().get("order_id"));
      return;
    }
    if (!"unpaid".equals(session.getPaymentStatus())) throw unreconciled();
    if (success
        && "checkout.session.completed".equals(type)
        && "complete".equals(session.getStatus())) {
      if (intent == null
          || "succeeded".equals(intent.getStatus())
          || intent.getAmountReceived() == null
          || intent.getAmountReceived() != 0) throw unreconciled();
      return; // Wait for the asynchronous result, without confirming or releasing stock.
    }
    if (intent != null
        && ("succeeded".equals(intent.getStatus())
            || "processing".equals(intent.getStatus())
            || intent.getAmountReceived() == null
            || intent.getAmountReceived() != 0)) throw unreconciled();
    if (success) {
      throw unreconciled();
    }
    boolean expired =
        "checkout.session.expired".equals(type) && "expired".equals(session.getStatus());
    boolean failed =
        "checkout.session.async_payment_failed".equals(type)
            && "complete".equals(session.getStatus());
    if ((!expired && !failed)
        || (!expired && intent == null)
        || (intent != null
            && !Set.of("requires_payment_method", "canceled").contains(intent.getStatus())))
      throw unreconciled();
    orders.failed(session.getMetadata().get("order_id"));
  }

  private void validateSession(String id, Session session) {
    if (session == null
        || !id.equals(session.getId())
        || !"payment".equals(session.getMode())
        || !Boolean.valueOf(liveMode).equals(session.getLivemode())
        || session.getAmountTotal() == null
        || session.getAmountTotal() < 1
        || session.getAmountTotal() > 99999999
        || !validCurrency(session.getCurrency())
        || !validMetadata(session.getMetadata())
        || !session.getAmountTotal().toString().equals(session.getMetadata().get("amount_minor"))
        || !session
            .getCurrency()
            .toUpperCase(Locale.ROOT)
            .equals(session.getMetadata().get("currency"))
        || !session.getMetadata().get("order_id").equals(session.getClientReferenceId()))
      throw unreconciled();
  }

  private void validateIntent(Session session, PaymentIntent intent) {
    if (intent.getId() == null
        || !intent.getId().matches("pi_[A-Za-z0-9]+")
        || intent.getStatus() == null
        || !intent.getId().equals(session.getPaymentIntent())
        || !Boolean.valueOf(liveMode).equals(intent.getLivemode())
        || !session.getAmountTotal().equals(intent.getAmount())
        || !session.getCurrency().equalsIgnoreCase(intent.getCurrency())
        || intent.getMetadata() == null
        || !Set.of(
                "integration",
                "order_id",
                "customer_id",
                "amount_minor",
                "currency",
                "checkout_key",
                "reservation_expires_at")
            .stream()
            .allMatch(key -> session.getMetadata().get(key).equals(intent.getMetadata().get(key))))
      throw unreconciled();
  }

  private boolean validCurrency(String currency) {
    return currency != null
        && currency.matches("[a-zA-Z]{3}")
        && Currency.getAvailableCurrencies().stream()
            .anyMatch(c -> c.getCurrencyCode().equalsIgnoreCase(currency));
  }

  private boolean validMetadata(Map<String, String> metadata) {
    if (metadata == null
        || !"gelatoflow-v1".equals(metadata.get("integration"))
        || !Ids.isUuid(metadata.get("order_id"))
        || !Ids.isUuid(metadata.get("customer_id"))
        || !Ids.isUuid(metadata.get("checkout_key"))) return false;
    try {
      Instant.parse(metadata.get("reservation_expires_at"));
      return true;
    } catch (java.time.DateTimeException | NullPointerException e) {
      return false;
    }
  }

  private ApiException unreconciled() {
    return new ApiException(
        HttpStatus.SERVICE_UNAVAILABLE,
        "PAYMENT_NOT_RECONCILED",
        "Checkout payment could not be reconciled; retry delivery");
  }
}
