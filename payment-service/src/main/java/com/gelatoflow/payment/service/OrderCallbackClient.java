package com.gelatoflow.payment.service;

import com.gelatoflow.payment.client.order.HttpOrderCallbackClient;
import com.gelatoflow.payment.client.order.OrderCallbackPort;
import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.exception.ApiException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Component;

/**
 * Client for notifying Order Service about payment transitions. If Order Service rejects a payment
 * with HTTP 409 Conflict (e.g. reservation expired or order cancelled), this client catches the
 * conflict and immediately triggers {@link RefundService} to refund the customer.
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
   * Notifies Order Service that a payment has succeeded. If Order Service rejects the transition
   * with HTTP 409 Conflict (reservation expired or order cancelled), the conflict is caught and
   * RefundService is immediately invoked to refund the customer.
   *
   * @param orderId UUID of the order
   * @param paymentIntentId Stripe PaymentIntent ID to refund in case of conflict
   */
  public void notifyPaymentSucceeded(String orderId, String paymentIntentId) {
    try {
      orderCallbackPort.succeeded(orderId);
    } catch (ApiException e) {
      if (isConflict(e)) {
        log.warn(
            "Order {} rejected payment with 409 conflict (reservation expired or cancelled); triggering automated refund for payment intent {}",
            orderId,
            paymentIntentId);
        if (paymentIntentId != null && !paymentIntentId.isBlank()) {
          refundService.refund(paymentIntentId);
        }
        return;
      }
      throw e;
    }
  }

  /**
   * Overload for notifying payment succeeded without a PaymentIntent ID.
   *
   * @param orderId UUID of the order
   */
  public void notifyPaymentSucceeded(String orderId) {
    notifyPaymentSucceeded(orderId, null);
  }

  /**
   * Notifies Order Service that a payment has failed.
   *
   * @param orderId UUID of the order
   */
  public void notifyPaymentFailed(String orderId) {
    orderCallbackPort.failed(orderId);
  }

  private boolean isConflict(ApiException e) {
    return HttpStatus.CONFLICT.equals(e.status()) || "ORDER_PAYMENT_CONFLICT".equals(e.code());
  }
}
