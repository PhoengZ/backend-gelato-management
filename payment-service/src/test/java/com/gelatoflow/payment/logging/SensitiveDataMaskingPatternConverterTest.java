package com.gelatoflow.payment.logging;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;

import ch.qos.logback.classic.spi.ILoggingEvent;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class SensitiveDataMaskingPatternConverterTest {
  private SensitiveDataMaskingPatternConverter converter;

  @BeforeEach
  void setUp() {
    converter = new SensitiveDataMaskingPatternConverter();
  }

  @Test
  void masksPaymentServiceTokenInHeaderAndConfigFormats() {
    String header =
        "Outgoing request with X-Payment-Service-Token: 994a37fd-35b9-4eb1-9988-75704d9c7bb7";
    assertThat(SensitiveDataMaskingPatternConverter.mask(header))
        .isEqualTo("Outgoing request with X-Payment-Service-Token: [REDACTED]");

    String keyVal = "Config loaded: X-Payment-Service-Token=secret-val-32bytes-long";
    assertThat(SensitiveDataMaskingPatternConverter.mask(keyVal))
        .isEqualTo("Config loaded: X-Payment-Service-Token=[REDACTED]");

    String json = "{\"X-Payment-Service-Token\": \"secret-json-token\"}";
    assertThat(SensitiveDataMaskingPatternConverter.mask(json))
        .isEqualTo("{\"X-Payment-Service-Token\": \"[REDACTED]\"}");
  }

  @Test
  void masksPaymentRequestToken() {
    String header = "Received PAYMENT_REQUEST_TOKEN: req-token-12345678";
    assertThat(SensitiveDataMaskingPatternConverter.mask(header))
        .isEqualTo("Received PAYMENT_REQUEST_TOKEN: [REDACTED]");

    String eqFormat = "PAYMENT_REQUEST_TOKEN=secret_request_token_abc";
    assertThat(SensitiveDataMaskingPatternConverter.mask(eqFormat))
        .isEqualTo("PAYMENT_REQUEST_TOKEN=[REDACTED]");

    String json = "{\"PAYMENT_REQUEST_TOKEN\": \"secret_request_token_abc\"}";
    assertThat(SensitiveDataMaskingPatternConverter.mask(json))
        .isEqualTo("{\"PAYMENT_REQUEST_TOKEN\": \"[REDACTED]\"}");
  }

  @Test
  void masksStripeSignatureHeader() {
    String logMsg =
        "Webhook request Stripe-Signature: t=1614000,v1=525768365d6a1ff8658d4b538257ac92f";
    assertThat(SensitiveDataMaskingPatternConverter.mask(logMsg))
        .isEqualTo("Webhook request Stripe-Signature: [REDACTED]");

    String json = "{\"Stripe-Signature\": \"t=1614000,v1=525768365d6a1ff8658d4b538257ac92f\"}";
    assertThat(SensitiveDataMaskingPatternConverter.mask(json))
        .isEqualTo("{\"Stripe-Signature\": \"[REDACTED]\"}");
  }

  @Test
  void masksStripeUrls() {
    String checkoutUrl =
        "Redirecting customer to https://checkout.stripe.com/c/pay/cs_test_a1b2c3d4e5 for session";
    assertThat(SensitiveDataMaskingPatternConverter.mask(checkoutUrl))
        .isEqualTo("Redirecting customer to [REDACTED] for session");

    String apiUrl = "Calling Stripe API at https://api.stripe.com/v1/refunds successfully";
    assertThat(SensitiveDataMaskingPatternConverter.mask(apiUrl))
        .isEqualTo("Calling Stripe API at [REDACTED] successfully");
  }

  @Test
  void masksAuthorizationBearerToken() {
    String logMsg = "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9";
    assertThat(SensitiveDataMaskingPatternConverter.mask(logMsg))
        .isEqualTo("Authorization: Bearer [REDACTED]");
  }

  @Test
  void preservesNonSensitiveMessages() {
    String harmless = "Order c19b830e-a733-41de-bf26-aeb123926e36 completed in 250ms";
    assertThat(SensitiveDataMaskingPatternConverter.mask(harmless)).isEqualTo(harmless);
  }

  @Test
  void handlesNullAndEmptyGracefully() {
    assertThat(SensitiveDataMaskingPatternConverter.mask(null)).isNull();
    assertThat(SensitiveDataMaskingPatternConverter.mask("")).isEmpty();
  }

  @Test
  void transformDelegatesProperlyToMask() {
    ILoggingEvent event = mock(ILoggingEvent.class);
    String raw = "X-Payment-Service-Token: raw-secret-key-123";
    assertThat(converter.transform(event, raw)).isEqualTo("X-Payment-Service-Token: [REDACTED]");
    assertThat(converter.transform(event, null)).isNull();
  }
}
