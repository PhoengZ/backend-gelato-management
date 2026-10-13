package com.gelatoflow.payment.logging;

import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.core.pattern.CompositeConverter;
import java.util.regex.Pattern;

/**
 * Logback pattern converter that masks sensitive data such as service tokens, Stripe signatures,
 * and Stripe URLs to comply with NFR-07 (Log Redaction).
 */
public class SensitiveDataMaskingPatternConverter extends CompositeConverter<ILoggingEvent> {

  private static final Pattern PAYMENT_SERVICE_TOKEN_PATTERN =
      Pattern.compile(
          "(?i)([\"']?X-Payment-Service-Token[\"']?\\s*[:=]\\s*[\"']?)([^\\s\"',;]+)([\"']?)");

  private static final Pattern PAYMENT_REQUEST_TOKEN_PATTERN =
      Pattern.compile(
          "(?i)([\"']?PAYMENT_REQUEST_TOKEN[\"']?\\s*[:=]\\s*[\"']?)([^\\s\"',;]+)([\"']?)");

  private static final Pattern STRIPE_SIGNATURE_PATTERN =
      Pattern.compile(
          "(?i)([\"']?Stripe-Signature[\"']?\\s*[:=]\\s*[\"']?)([^\\r\\n\"']+)([\"']?)");

  private static final Pattern STRIPE_URL_PATTERN =
      Pattern.compile("https?://(?:[a-zA-Z0-9-]+\\.)*stripe\\.com[^\\s\"'>)]*");

  private static final Pattern AUTHORIZATION_BEARER_PATTERN =
      Pattern.compile("(?i)(Authorization\\s*[:=]\\s*Bearer\\s+)([^\\s\"',;]+)");

  @Override
  protected String transform(ILoggingEvent event, String in) {
    if (in == null || in.isEmpty()) {
      return in;
    }
    return mask(in);
  }

  /**
   * Masks sensitive fields in the provided log message.
   *
   * @param input Raw log message
   * @return Redacted log message with sensitive information masked
   */
  public static String mask(String input) {
    if (input == null || input.isEmpty()) {
      return input;
    }
    String masked = input;
    masked = PAYMENT_SERVICE_TOKEN_PATTERN.matcher(masked).replaceAll("$1[REDACTED]$3");
    masked = PAYMENT_REQUEST_TOKEN_PATTERN.matcher(masked).replaceAll("$1[REDACTED]$3");
    masked = STRIPE_SIGNATURE_PATTERN.matcher(masked).replaceAll("$1[REDACTED]$3");
    masked = AUTHORIZATION_BEARER_PATTERN.matcher(masked).replaceAll("$1[REDACTED]");
    masked = STRIPE_URL_PATTERN.matcher(masked).replaceAll("[REDACTED]");
    return masked;
  }
}
