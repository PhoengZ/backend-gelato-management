package com.gelatoflow.payment.client.stripe;

import com.gelatoflow.payment.exception.ApiException;
import com.stripe.StripeClient;
import com.stripe.exception.StripeException;
import com.stripe.model.checkout.Session;
import com.stripe.net.RequestOptions;
import com.stripe.param.checkout.SessionRetrieveParams;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Component;

@Component
public class StripeCheckoutWebhookGateway implements CheckoutWebhookPort {
  private final StripeClient client;

  public StripeCheckoutWebhookGateway(StripeClient client) {
    this.client = client;
  }

  @Override
  public Session retrieve(String sessionId) {
    try {
      return client
          .v1()
          .checkout()
          .sessions()
          .retrieve(
              sessionId,
              SessionRetrieveParams.builder().addExpand("payment_intent").build(),
              RequestOptions.builder()
                  .setConnectTimeout(1000)
                  .setReadTimeout(4000)
                  .setMaxNetworkRetries(0)
                  .build());
    } catch (StripeException e) {
      throw new ApiException(
          HttpStatus.BAD_GATEWAY,
          "PAYMENT_PROVIDER_ERROR",
          "Checkout payment could not be reconciled; retry delivery");
    }
  }
}
