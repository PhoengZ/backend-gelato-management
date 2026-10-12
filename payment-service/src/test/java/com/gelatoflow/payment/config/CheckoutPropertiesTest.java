package com.gelatoflow.payment.config;

import static com.gelatoflow.payment.testsupport.CheckoutFixtures.*;
import static org.assertj.core.api.Assertions.*;

import jakarta.validation.Validation;
import org.junit.jupiter.api.Test;

class CheckoutPropertiesTest {
  @Test
  void configurationFailsClosedForMissingCredentialsTlsFilesAndUntrustedRedirects() {
    try (var factory = Validation.buildDefaultValidatorFactory()) {
      var validator = factory.getValidator();
      assertThat(validator.validate(checkoutProperties())).isEmpty();
      assertThat(
              validator.validate(
                  new CheckoutProperties(
                      true,
                      50057,
                      "",
                      false,
                      "",
                      "",
                      "https://example.com/success",
                      "https://example.com/cancel")))
          .isNotEmpty();
      assertThat(
              validator.validate(
                  new CheckoutProperties(
                      true,
                      50057,
                      TOKEN,
                      true,
                      "",
                      "",
                      "https://example.com/success",
                      "https://example.com/cancel")))
          .isNotEmpty();
      assertThat(
              validator.validate(
                  new CheckoutProperties(
                      true,
                      50057,
                      TOKEN,
                      false,
                      "",
                      "",
                      "http://attacker.example/success",
                      "https://example.com/cancel")))
          .isNotEmpty();
      assertThat(validator.validate(new CheckoutProperties(false, 50057, "", true, "", "", "", "")))
          .isEmpty();
    }
  }
}
