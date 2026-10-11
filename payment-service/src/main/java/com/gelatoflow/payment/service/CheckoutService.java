package com.gelatoflow.payment.service;

import com.gelatoflow.contracts.payment.v1.CreateCheckoutSessionRequest;
import com.gelatoflow.contracts.payment.v1.CreateCheckoutSessionResponse;
import com.gelatoflow.payment.client.stripe.CheckoutPort;
import com.gelatoflow.payment.client.stripe.StripeCheckoutGateway;
import com.gelatoflow.payment.config.PaymentProperties;
import com.gelatoflow.payment.validation.Ids;
import io.grpc.Status;
import java.net.URI;
import java.time.Clock;
import java.time.Instant;
import java.util.Currency;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.stereotype.Service;

@Service
@ConditionalOnProperty(
    name = "payment.checkout.enabled",
    havingValue = "true",
    matchIfMissing = true)
public class CheckoutService {
  private final CheckoutPort stripe;
  private final boolean liveMode;
  private final Clock clock;

  @org.springframework.beans.factory.annotation.Autowired
  public CheckoutService(CheckoutPort stripe, PaymentProperties properties) {
    this(stripe, properties, Clock.systemUTC());
  }

  public CheckoutService(CheckoutPort stripe, PaymentProperties properties, Clock clock) {
    this.stripe = stripe;
    this.liveMode = properties.stripe().liveMode();
    this.clock = clock;
  }

  public CreateCheckoutSessionResponse create(CreateCheckoutSessionRequest request) {
    if (!Ids.isUuid(request.getOrderId())
        || !Ids.isUuid(request.getCustomerId())
        || !Ids.isUuid(request.getIdempotencyKey())) {
      throw Status.INVALID_ARGUMENT
          .withDescription("Order, customer and checkout key must be non-nil UUIDs")
          .asRuntimeException();
    }
    if (request.getAmountMinor() < 1
        || request.getAmountMinor() > 99999999
        || !request.getCurrency().matches("[A-Z]{3}")
        || Currency.getAvailableCurrencies().stream()
            .noneMatch(c -> c.getCurrencyCode().equals(request.getCurrency()))) {
      throw Status.INVALID_ARGUMENT
          .withDescription("A positive minor-unit amount and ISO currency are required")
          .asRuntimeException();
    }
    var expiry = request.getReservationExpiresAt();
    if (!request.hasReservationExpiresAt()
        || expiry.getSeconds() < -62135596800L
        || expiry.getSeconds() > 253402300799L
        || expiry.getNanos() < 0
        || expiry.getNanos() > 999999999) {
      throw Status.INVALID_ARGUMENT
          .withDescription("A valid reservation deadline is required")
          .asRuntimeException();
    }
    var deadline = Instant.ofEpochSecond(expiry.getSeconds(), expiry.getNanos());
    if (!deadline.isAfter(clock.instant())) {
      throw Status.FAILED_PRECONDITION
          .withDescription("The stock reservation has expired")
          .asRuntimeException();
    }
    var normalized =
        request.toBuilder()
            .setOrderId(UUID.fromString(request.getOrderId()).toString())
            .setCustomerId(UUID.fromString(request.getCustomerId()).toString())
            .setIdempotencyKey(UUID.fromString(request.getIdempotencyKey()).toString())
            .build();
    var session = stripe.create(normalized);
    if (session == null
        || session.getId() == null
        || !session.getId().matches("cs_[A-Za-z0-9_]+")
        || !Boolean.valueOf(liveMode).equals(session.getLivemode())
        || !Long.valueOf(request.getAmountMinor()).equals(session.getAmountTotal())
        || !request.getCurrency().equalsIgnoreCase(session.getCurrency())
        || !normalized.getOrderId().equals(session.getClientReferenceId())
        || session.getMetadata() == null
        || !StripeCheckoutGateway.metadata(normalized).entrySet().stream()
            .allMatch(e -> e.getValue().equals(session.getMetadata().get(e.getKey())))) {
      throw Status.UNAVAILABLE
          .withDescription("Stripe returned inconsistent checkout details")
          .asRuntimeException();
    }
    if (!"payment".equals(session.getMode())) {
      throw Status.UNAVAILABLE
          .withDescription("Stripe returned an unexpected checkout mode")
          .asRuntimeException();
    }
    if (!"open".equals(session.getStatus()) || !"unpaid".equals(session.getPaymentStatus())) {
      throw Status.FAILED_PRECONDITION
          .withDescription("The Checkout Session is no longer payable")
          .asRuntimeException();
    }
    if (!deadline.isAfter(clock.instant())) {
      throw Status.FAILED_PRECONDITION
          .withDescription("The stock reservation expired during checkout creation")
          .asRuntimeException();
    }
    try {
      var url = URI.create(session.getUrl());
      if (!"https".equals(url.getScheme())
          || !"checkout.stripe.com".equals(url.getHost())
          || url.getUserInfo() != null) {
        throw new IllegalArgumentException();
      }
    } catch (IllegalArgumentException | NullPointerException e) {
      throw Status.UNAVAILABLE
          .withDescription("Stripe returned an invalid checkout URL")
          .asRuntimeException();
    }
    return CreateCheckoutSessionResponse.newBuilder()
        .setCheckoutSessionId(session.getId())
        .setCheckoutUrl(session.getUrl())
        .build();
  }
}
