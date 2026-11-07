package com.gelatoflow.payment.service;

import com.gelatoflow.payment.client.order.HttpOrderCallbackClient;
import com.gelatoflow.payment.client.order.OrderCallbackPort;
import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.exception.ApiException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

/**
 * Reports payment transitions to Order Service and refunds a charge that Order definitively
 * rejected. Only {@link HttpOrderCallbackClient#PAYMENT_REJECTED} proves the order can never be
 * paid. A 409 is ambiguous, because the order may still become PAID, so it propagates and Stripe
 * retries the webhook.
 */
@Component
public class OrderCallbackClient {
  private static final Logger log = LoggerFactory.getLogger(OrderCallbackClient.class);
  private final OrderCallbackPort orderCallbackPort;
  private final RefundService refundService;

  @Autowired
  public OrderCallbackClient(OrderCallbackPort orderCallbackPort, RefundService refundService) {
    this.orderCallbackPort = orderCallbackPort;
    this.refundService = refundService;
  }

  public OrderCallbackClient(OrderCallbackProperties properties, RefundService refundService) {
    this(new HttpOrderCallbackClient(properties), refundService);
  }

  /**
   * Notifies Order Service that a payment has succeeded, refunding it if Order rejects it.
   *
   * @param orderId UUID of the order
   * @param paymentIntentId Stripe PaymentIntent ID to refund on a definitive rejection
   */
  public void notifyPaymentSucceeded(String orderId, String paymentIntentId) {
    try {
      orderCallbackPort.succeeded(orderId);
    } catch (ApiException e) {
      if (!HttpOrderCallbackClient.PAYMENT_REJECTED.equals(e.code())
          || paymentIntentId == null
          || paymentIntentId.isBlank()) throw e;
      log.warn("Order {} rejected payment; refunding intent {}", orderId, paymentIntentId);
      refundService.refund(paymentIntentId);
    }
  }

  /**
   * Notifies Order Service that a payment has failed.
   *
   * @param orderId UUID of the order
   */
  public void notifyPaymentFailed(String orderId) {
    orderCallbackPort.failed(orderId);
  }
}
