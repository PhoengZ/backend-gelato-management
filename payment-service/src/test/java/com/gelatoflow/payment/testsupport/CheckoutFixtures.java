package com.gelatoflow.payment.testsupport;

import static com.gelatoflow.payment.testsupport.Fixtures.*;

import com.gelatoflow.contracts.payment.v1.CreateCheckoutSessionRequest;
import com.gelatoflow.payment.client.stripe.StripeCheckoutGateway;
import com.gelatoflow.payment.config.CheckoutProperties;
import com.google.protobuf.Timestamp;
import com.stripe.model.checkout.Session;
import java.time.Instant;

public final class CheckoutFixtures {
  private CheckoutFixtures() {}

  public static final String TOKEN = "order-to-payment-test-token-at-least-32-bytes";
  public static final String KEY = "33ed7b7b-7441-473c-a9bc-0de7c899275c";
  public static final String URL = "https://checkout.stripe.com/c/pay/cs_test_fixture";

  public static CreateCheckoutSessionRequest request() {
    return CreateCheckoutSessionRequest.newBuilder()
        .setOrderId(ORDER)
        .setCustomerId(CUSTOMER)
        .setAmountMinor(25000)
        .setCurrency("THB")
        .setIdempotencyKey(KEY)
        .setReservationExpiresAt(
            Timestamp.newBuilder().setSeconds(Instant.now().plusSeconds(600).getEpochSecond()))
        .build();
  }

  public static Session session(CreateCheckoutSessionRequest request) {
    var session = new Session();
    session.setId("cs_test_fixture");
    session.setUrl(URL);
    session.setStatus("open");
    session.setMode("payment");
    session.setPaymentStatus("unpaid");
    session.setLivemode(false);
    session.setAmountTotal(request.getAmountMinor());
    session.setCurrency("thb");
    session.setClientReferenceId(request.getOrderId());
    session.setMetadata(StripeCheckoutGateway.metadata(request));
    return session;
  }

  public static CheckoutProperties checkoutProperties() {
    return new CheckoutProperties(
        true,
        0,
        TOKEN,
        false,
        "",
        "",
        "http://localhost:3000/success?session_id={CHECKOUT_SESSION_ID}",
        "http://localhost:3000/cancel");
  }
}
