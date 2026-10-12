package com.gelatoflow.payment.client.stripe;

import com.stripe.model.checkout.Session;

public interface CheckoutWebhookPort {
  Session retrieve(String sessionId);
}
