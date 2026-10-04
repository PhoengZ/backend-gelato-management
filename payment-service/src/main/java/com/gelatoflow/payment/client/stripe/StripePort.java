package com.gelatoflow.payment.client.stripe;

import com.stripe.model.PaymentIntent;

public interface StripePort {
  PaymentIntent create(String orderId, String customerId, long amountMinor, String currency);

  PaymentIntent retrieve(String intentId);
}
