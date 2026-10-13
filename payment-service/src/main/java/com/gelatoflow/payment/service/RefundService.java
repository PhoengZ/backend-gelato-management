package com.gelatoflow.payment.service;

import com.gelatoflow.payment.config.PaymentProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.stripe.exception.IdempotencyException;
import com.stripe.exception.StripeException;
import com.stripe.model.Refund;
import com.stripe.net.RequestOptions;
import com.stripe.param.RefundCreateParams;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Service;

@Service
public class RefundService {
  private static final Logger log = LoggerFactory.getLogger(RefundService.class);
  private final PaymentProperties properties;

  public RefundService(PaymentProperties properties) {
    this.properties = properties;
  }

  /**
   * Automates Stripe refund for a payment intent. Uses a deterministic idempotency key
   * 'refund-{paymentIntentId}' to prevent duplicate refunds.
   *
   * @param paymentIntentId Stripe PaymentIntent ID
   * @return the created or replayed Refund object
   */
  public Refund refund(String paymentIntentId) {
    if (paymentIntentId == null || paymentIntentId.isBlank()) {
      throw new ApiException(
          HttpStatus.BAD_REQUEST, "INVALID_PAYMENT_INTENT", "PaymentIntent ID cannot be empty");
    }

    String idempotencyKey = "refund-" + paymentIntentId;
    var params = RefundCreateParams.builder().setPaymentIntent(paymentIntentId).build();

    var optionsBuilder = RequestOptions.builder().setIdempotencyKey(idempotencyKey);
    if (properties != null
        && properties.stripe() != null
        && properties.stripe().secretKey() != null) {
      optionsBuilder.setApiKey(properties.stripe().secretKey());
    }
    var options = optionsBuilder.build();

    try {
      log.info(
          "Creating automated refund for PaymentIntent {} with idempotency key {}",
          paymentIntentId,
          idempotencyKey);
      Refund refund = Refund.create(params, options);
      log.info(
          "Refund created successfully: id={}, status={}",
          refund != null ? refund.getId() : null,
          refund != null ? refund.getStatus() : null);
      return refund;
    } catch (IdempotencyException e) {
      log.warn(
          "Idempotent refund replay for PaymentIntent {}: {}", paymentIntentId, e.getMessage());
      throw new ApiException(
          HttpStatus.CONFLICT,
          "REFUND_CONFLICT",
          "Refund request conflicts with a previous attempt");
    } catch (StripeException e) {
      log.error(
          "Failed to create refund for PaymentIntent {}: {}", paymentIntentId, e.getMessage());
      if (Integer.valueOf(409).equals(e.getStatusCode())) {
        throw new ApiException(
            HttpStatus.CONFLICT,
            "REFUND_CONFLICT",
            "Refund request conflicts with a previous attempt");
      }
      throw new ApiException(
          HttpStatus.BAD_GATEWAY,
          "PAYMENT_PROVIDER_ERROR",
          "Payment provider is temporarily unavailable");
    }
  }

  public Refund refundPayment(String paymentIntentId) {
    return refund(paymentIntentId);
  }
}
