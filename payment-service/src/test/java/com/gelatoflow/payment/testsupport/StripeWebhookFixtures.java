package com.gelatoflow.payment.testsupport;

import static com.gelatoflow.payment.testsupport.Fixtures.WEBHOOK_SECRET;

import java.nio.charset.StandardCharsets;
import java.util.HexFormat;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

public final class StripeWebhookFixtures {
  private StripeWebhookFixtures() {}

  public static String payload(String type) {
    return "{\"id\":\"evt_fixture\",\"object\":\"event\",\"created\":1700000000,\"livemode\":false,\"type\":\""
        + type
        + "\",\"data\":{\"object\":{\"id\":\"pi_fixture\",\"object\":\"payment_intent\"}}}";
  }

  public static String sign(String payload, long timestamp) throws Exception {
    var mac = Mac.getInstance("HmacSHA256");
    mac.init(new SecretKeySpec(WEBHOOK_SECRET.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
    return "t="
        + timestamp
        + ",v1="
        + HexFormat.of()
            .formatHex(mac.doFinal((timestamp + "." + payload).getBytes(StandardCharsets.UTF_8)));
  }
}
