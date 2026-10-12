package com.gelatoflow.payment.config;

import jakarta.validation.constraints.AssertTrue;
import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.validation.annotation.Validated;

@Validated
@ConfigurationProperties("payment.order-callback")
public record OrderCallbackProperties(
    boolean enabled,
    String baseUrl,
    String serviceToken,
    boolean allowPlaintext,
    @Min(100) @Max(30000) long timeoutMs) {
  @AssertTrue(
      message =
          "Order callbacks require a machine token of at least 32 UTF-8 bytes without whitespace")
  public boolean isTokenValid() {
    return !enabled
        || (serviceToken != null
            && !serviceToken.isBlank()
            && serviceToken.getBytes(StandardCharsets.UTF_8).length >= 32
            && serviceToken.codePoints().noneMatch(Character::isWhitespace));
  }

  @AssertTrue(
      message =
          "Order callback URL must be an HTTPS origin; explicitly allow HTTP only for isolated development")
  public boolean isUrlValid() {
    if (!enabled) return true;
    try {
      var uri = URI.create(baseUrl);
      return uri.getHost() != null
          && uri.getUserInfo() == null
          && uri.getQuery() == null
          && uri.getFragment() == null
          && (uri.getPath().isEmpty() || "/".equals(uri.getPath()))
          && (uri.getPort() == -1 || (uri.getPort() > 0 && uri.getPort() <= 65535))
          && ("https".equals(uri.getScheme())
              || (allowPlaintext && "http".equals(uri.getScheme())));
    } catch (IllegalArgumentException | NullPointerException e) {
      return false;
    }
  }
}
