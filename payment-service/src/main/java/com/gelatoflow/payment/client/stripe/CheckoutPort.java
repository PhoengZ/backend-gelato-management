package com.gelatoflow.payment.client.stripe;

import com.gelatoflow.contracts.payment.v1.CreateCheckoutSessionRequest;
import com.stripe.model.checkout.Session;

public interface CheckoutPort {
  Session create(CreateCheckoutSessionRequest request);
}
