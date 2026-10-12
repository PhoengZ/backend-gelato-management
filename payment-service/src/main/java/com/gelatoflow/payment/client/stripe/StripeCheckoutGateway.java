package com.gelatoflow.payment.client.stripe;

import com.gelatoflow.contracts.payment.v1.CreateCheckoutSessionRequest;
import com.gelatoflow.payment.config.CheckoutProperties;
import com.stripe.StripeClient;
import com.stripe.exception.IdempotencyException;
import com.stripe.exception.StripeException;
import com.stripe.model.checkout.Session;
import com.stripe.net.RequestOptions;
import com.stripe.param.checkout.SessionCreateParams;
import io.grpc.Status;
import java.time.Instant;
import java.util.Locale;
import java.util.Map;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.stereotype.Component;

@Component
@ConditionalOnProperty(
    name = "payment.checkout.enabled",
    havingValue = "true",
    matchIfMissing = true)
public class StripeCheckoutGateway implements CheckoutPort {
  private final StripeClient client;
  private final CheckoutProperties properties;

  public StripeCheckoutGateway(StripeClient client, CheckoutProperties properties) {
    this.client = client;
    this.properties = properties;
  }

  public static Map<String, String> metadata(CreateCheckoutSessionRequest request) {
    var expiry = request.getReservationExpiresAt();
    return Map.of(
        "integration", "gelatoflow-v1",
        "order_id", request.getOrderId(),
        "customer_id", request.getCustomerId(),
        "amount_minor", Long.toString(request.getAmountMinor()),
        "currency", request.getCurrency(),
        "checkout_key", request.getIdempotencyKey(),
        "reservation_expires_at",
            Instant.ofEpochSecond(expiry.getSeconds(), expiry.getNanos()).toString());
  }

  @Override
  public Session create(CreateCheckoutSessionRequest request) {
    var metadata = metadata(request);
    var params =
        SessionCreateParams.builder()
            .setMode(SessionCreateParams.Mode.PAYMENT)
            .addAllowedPaymentMethodType(SessionCreateParams.AllowedPaymentMethodType.CARD)
            .setClientReferenceId(request.getOrderId())
            .setSuccessUrl(properties.successUrl())
            .setCancelUrl(properties.cancelUrl())
            .putAllMetadata(metadata)
            .setPaymentIntentData(
                SessionCreateParams.PaymentIntentData.builder().putAllMetadata(metadata).build())
            .addLineItem(
                SessionCreateParams.LineItem.builder()
                    .setQuantity(1L)
                    .setPriceData(
                        SessionCreateParams.LineItem.PriceData.builder()
                            .setCurrency(request.getCurrency().toLowerCase(Locale.ROOT))
                            .setUnitAmount(request.getAmountMinor())
                            .setProductData(
                                SessionCreateParams.LineItem.PriceData.ProductData.builder()
                                    .setName("GelatoFlow order")
                                    .build())
                            .build())
                    .build())
            // Do not derive expires_at from the current time: retries must send identical
            // parameters.
            // Inventory's deadline is metadata, not Stripe's minimum 30-minute session lifetime.
            .build();
    var options =
        RequestOptions.builder()
            .setIdempotencyKey("gelatoflow-checkout-v1-" + request.getIdempotencyKey())
            .setConnectTimeout(1000)
            .setReadTimeout(4000)
            .setMaxNetworkRetries(0)
            .build();
    try {
      var session = client.v1().checkout().sessions().create(params, options);
      if (session == null
          || session.getId() == null
          || !session.getId().matches("cs_[A-Za-z0-9_]+")) {
        throw Status.UNAVAILABLE
            .withDescription("Stripe returned an invalid session identifier")
            .asRuntimeException();
      }
      // The idempotent create response is a snapshot; inspect current state on every replay.
      var current =
          client
              .v1()
              .checkout()
              .sessions()
              .retrieve(
                  session.getId(),
                  RequestOptions.builder()
                      .setConnectTimeout(1000)
                      .setReadTimeout(4000)
                      .setMaxNetworkRetries(0)
                      .build());
      if (current == null || !session.getId().equals(current.getId())) {
        throw Status.UNAVAILABLE
            .withDescription("Stripe returned inconsistent session identifiers")
            .asRuntimeException();
      }
      return current;
    } catch (IdempotencyException e) {
      throw Status.ALREADY_EXISTS
          .withDescription("Checkout key conflicts with immutable request facts")
          .asRuntimeException();
    } catch (StripeException e) {
      if (Integer.valueOf(409).equals(e.getStatusCode())) {
        throw Status.ABORTED
            .withDescription("Checkout creation is in progress; replay the same request")
            .asRuntimeException();
      }
      throw Status.UNAVAILABLE
          .withDescription("Stripe checkout is temporarily unavailable; replay the same request")
          .asRuntimeException();
    }
  }
}
