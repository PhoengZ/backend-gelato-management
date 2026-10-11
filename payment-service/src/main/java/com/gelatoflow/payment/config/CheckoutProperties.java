package com.gelatoflow.payment.config;

import jakarta.validation.constraints.AssertTrue;
import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import java.net.URI;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.validation.annotation.Validated;

@Validated
@ConfigurationProperties("payment.checkout")
public record CheckoutProperties(
    boolean enabled,
    @Min(0) @Max(65535) int port,
    String requestToken,
    boolean tls,
    String certificate,
    String privateKey,
    String successUrl,
    String cancelUrl) {
  @AssertTrue(message = "Checkout requires a dedicated machine token of at least 32 UTF-8 bytes")
  public boolean isTokenValid() {
    return !enabled
        || (requestToken != null
            && !requestToken.isBlank()
            && requestToken.getBytes(java.nio.charset.StandardCharsets.UTF_8).length >= 32);
  }

  @AssertTrue(message = "Checkout TLS requires certificate and private-key PEM paths")
  public boolean isTlsValid() {
    return !enabled
        || !tls
        || (certificate != null
            && !certificate.isBlank()
            && privateKey != null
            && !privateKey.isBlank());
  }

  @AssertTrue(
      message = "Checkout redirects must be HTTPS; plaintext development permits loopback HTTP")
  public boolean isRedirectsValid() {
    return !enabled || (validRedirect(successUrl) && validRedirect(cancelUrl));
  }

  private boolean validRedirect(String value) {
    if (value == null || value.isBlank()) return false;
    try {
      var uri = URI.create(value.replace("{CHECKOUT_SESSION_ID}", "session"));
      if (uri.getHost() == null || uri.getUserInfo() != null || uri.getFragment() != null)
        return false;
      return "https".equals(uri.getScheme())
          || (!tls
              && "http".equals(uri.getScheme())
              && java.util.Set.of("localhost", "127.0.0.1", "[::1]").contains(uri.getHost()));
    } catch (IllegalArgumentException e) {
      return false;
    }
  }
}
