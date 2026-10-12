package com.gelatoflow.payment.config;

import jakarta.validation.Valid;
import jakarta.validation.constraints.*;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.validation.annotation.Validated;

@Validated
@ConfigurationProperties("payment")
public record PaymentProperties(
    @Valid @NotNull Stripe stripe, @Valid @NotNull Order order, @Valid @NotNull Jwt jwt) {
  public record Stripe(
      @NotBlank @Pattern(regexp = "sk_(test|live)_.+") String secretKey,
      @NotBlank @Pattern(regexp = "whsec_.+") String webhookSecret,
      boolean liveMode) {
    @AssertTrue(message = "Stripe key must match configured live mode")
    public boolean isModeConsistent() {
      return secretKey != null && secretKey.startsWith(liveMode ? "sk_live_" : "sk_test_");
    }
  }

  public record Order(
      @NotBlank String target,
      @NotBlank String serviceToken,
      boolean tls,
      @Min(100) @Max(30000) long deadlineMs) {}

  public record Jwt(@NotBlank String secret, @NotBlank String issuer, @NotBlank String audience) {
    @AssertTrue(message = "JWT secret must have at least 32 UTF-8 bytes")
    public boolean isSecretStrongEnough() {
      return secret != null
          && secret.getBytes(java.nio.charset.StandardCharsets.UTF_8).length >= 32;
    }
  }
}
