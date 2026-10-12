package com.gelatoflow.payment.testsupport;

import com.gelatoflow.contracts.order.v1.GetOrderDetailsResponse;
import com.gelatoflow.payment.config.PaymentProperties;
import com.stripe.model.PaymentIntent;
import java.util.Map;

public final class Fixtures {
  private Fixtures() {}

  public static final String ORDER = "c19b830e-a733-41de-bf26-aeb123926e36";
  public static final String CUSTOMER = "c90e35eb-4116-4d94-88ba-d4da278cda59";
  public static final String OTHER = "e3891fae-621e-4f99-97e9-75a0d8240bf4";
  public static final String SECRET = "test-signing-secret-at-least-32-bytes";
  public static final String WEBHOOK_SECRET = "whsec_test_fixture";

  public static PaymentProperties properties() {
    return new PaymentProperties(
        new PaymentProperties.Stripe("sk_test_fixture", WEBHOOK_SECRET, false),
        new PaymentProperties.Order("localhost:50054", "test-machine-token", false, 500),
        new PaymentProperties.Jwt(SECRET, "gelatoflow-auth", "gelatoflow-api"));
  }

  public static GetOrderDetailsResponse order() {
    return GetOrderDetailsResponse.newBuilder()
        .setOrderId(ORDER)
        .setCustomerId(CUSTOMER)
        .setStatus("PENDING_PAYMENT")
        .setTotalAmountMinor(25000)
        .setCurrency("THB")
        .build();
  }

  public static PaymentIntent intent(String status) {
    var intent = new PaymentIntent();
    intent.setId("pi_fixture");
    intent.setClientSecret("pi_fixture_secret_fixture");
    intent.setAmount(25000L);
    intent.setAmountReceived("succeeded".equals(status) ? 25000L : 0L);
    intent.setCurrency("thb");
    intent.setLivemode(false);
    intent.setStatus(status);
    intent.setMetadata(
        Map.of("order_id", ORDER, "customer_id", CUSTOMER, "integration", "gelatoflow-v1"));
    return intent;
  }
}
