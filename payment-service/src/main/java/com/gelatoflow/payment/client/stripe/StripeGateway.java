package com.gelatoflow.payment.client.stripe;

import com.gelatoflow.payment.exception.ApiException;
import com.stripe.StripeClient;
import com.stripe.exception.StripeException;
import com.stripe.model.PaymentIntent;
import com.stripe.net.RequestOptions;
import com.stripe.param.PaymentIntentCreateParams;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Component;

@Component
public class StripeGateway implements StripePort {
  private final StripeClient client;

  public StripeGateway(StripeClient client) {
    this.client = client;
  }

  @Override
  public PaymentIntent create(
      String orderId, String customerId, long amountMinor, String currency) {
    var params =
        PaymentIntentCreateParams.builder()
            .setAmount(amountMinor)
            .setCurrency(currency.toLowerCase(java.util.Locale.ROOT))
            .addAllowedPaymentMethodType(PaymentIntentCreateParams.AllowedPaymentMethodType.CARD)
            .putMetadata("order_id", orderId)
            .putMetadata("customer_id", customerId)
            .putMetadata("integration", "gelatoflow-v1")
            .build();
    var options =
        RequestOptions.builder().setIdempotencyKey("gelatoflow-payment-v1-" + orderId).build();
    try {
      return client.v1().paymentIntents().create(params, options);
    } catch (StripeException e) {
      throw failure(e);
    }
  }

  @Override
  public PaymentIntent retrieve(String intentId) {
    try {
      return client.v1().paymentIntents().retrieve(intentId);
    } catch (StripeException e) {
      throw failure(e);
    }
  }

  private ApiException failure(StripeException e) {
    if (e instanceof com.stripe.exception.IdempotencyException
        || Integer.valueOf(409).equals(e.getStatusCode())) {
      return new ApiException(
          HttpStatus.CONFLICT,
          "PAYMENT_CONFLICT",
          "Payment request conflicts with a previous attempt");
    }
    return new ApiException(
        HttpStatus.BAD_GATEWAY,
        "PAYMENT_PROVIDER_ERROR",
        "Payment provider is temporarily unavailable");
  }
}
