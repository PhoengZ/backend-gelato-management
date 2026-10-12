package com.gelatoflow.payment.config;

import static org.assertj.core.api.Assertions.*;

import jakarta.validation.Validation;
import org.junit.jupiter.api.Test;

class OrderCallbackPropertiesTest {
  @Test
  void failsClosedForWeakCredentialsUnsafeOriginsAndUnboundedTimeouts() {
    try (var factory = Validation.buildDefaultValidatorFactory()) {
      var validator = factory.getValidator();
      var token = "payment-to-order-token-at-least-32-bytes";
      assertThat(
              validator.validate(
                  new OrderCallbackProperties(true, "https://order.example", token, false, 5000)))
          .isEmpty();
      assertThat(
              validator.validate(
                  new OrderCallbackProperties(
                      true, "http://order-service:3003", token, true, 5000)))
          .isEmpty();
      for (var url :
          new String[] {
            "http://order.example",
            "https://user@order.example",
            "https://order.example?token=secret",
            "https://order.example/#secret",
            "https://order.example/api",
            "file:///orders"
          }) {
        assertThat(validator.validate(new OrderCallbackProperties(true, url, token, false, 5000)))
            .isNotEmpty();
      }
      for (var secret : new String[] {"", "short", token + "\n"}) {
        assertThat(
                validator.validate(
                    new OrderCallbackProperties(
                        true, "https://order.example", secret, false, 5000)))
            .isNotEmpty();
      }
      assertThat(
              validator.validate(
                  new OrderCallbackProperties(true, "https://order.example", token, false, 30001)))
          .isNotEmpty();
      assertThat(validator.validate(new OrderCallbackProperties(false, "", "", false, 5000)))
          .isEmpty();
    }
  }
}
