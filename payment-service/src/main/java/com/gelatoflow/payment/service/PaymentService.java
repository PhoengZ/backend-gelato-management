package com.gelatoflow.payment.service;

import com.gelatoflow.contracts.order.v1.*;
import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.gelatoflow.payment.config.PaymentProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.gelatoflow.payment.validation.Ids;
import com.stripe.model.PaymentIntent;
import java.util.*;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Service;

@Service
public class PaymentService {
  private final OrderPort orders;
  private final StripePort stripe;
  private final boolean liveMode;

  public PaymentService(OrderPort orders, StripePort stripe, PaymentProperties properties) {
    this.orders = orders;
    this.stripe = stripe;
    this.liveMode = properties.stripe().liveMode();
  }

  public record IntentResponse(
      String paymentIntentId, String clientSecret, long amountMinor, String currency) {}

  public IntentResponse create(String orderId, String customerId) {
    if (!Ids.isUuid(orderId))
      throw new ApiException(
          HttpStatus.BAD_REQUEST, "INVALID_ORDER_ID", "order_id must be a non-nil UUID");
    orderId = UUID.fromString(orderId).toString();
    var order = orders.get(orderId);
    if (!orderId.equals(order.getOrderId())) throw invalidOrder();
    if (!customerId.equalsIgnoreCase(order.getCustomerId())) {
      // Do not reveal another customer's order or client secret.
      throw new ApiException(HttpStatus.NOT_FOUND, "ORDER_NOT_FOUND", "Order does not exist");
    }
    if (!"PENDING_PAYMENT".equals(order.getStatus())) {
      throw new ApiException(
          HttpStatus.CONFLICT, "ORDER_NOT_PAYABLE", "Order is not pending payment");
    }
    var currency = order.getCurrency();
    if (order.getTotalAmountMinor() <= 0
        || order.getTotalAmountMinor() > 99999999
        || !currency.matches("[A-Z]{3}")
        || !Currency.getAvailableCurrencies().stream()
            .anyMatch(c -> c.getCurrencyCode().equals(currency))) {
      throw invalidOrder();
    }
    var intent =
        order.getPaymentIntentId().isBlank()
            ? stripe.create(
                orderId,
                UUID.fromString(customerId).toString(),
                order.getTotalAmountMinor(),
                currency)
            : stripe.retrieve(order.getPaymentIntentId());
    validateIntent(intent);
    if (!orderId.equals(intent.getMetadata().get("order_id"))
        || !customerId.equalsIgnoreCase(intent.getMetadata().get("customer_id"))
        || intent.getAmount() != order.getTotalAmountMinor()
        || !currency.equalsIgnoreCase(intent.getCurrency())
        || (!order.getPaymentIntentId().isBlank()
            && !order.getPaymentIntentId().equals(intent.getId()))) {
      throw new ApiException(
          HttpStatus.CONFLICT, "PAYMENT_MISMATCH", "Provider payment does not match the order");
    }
    if (!Set.of("requires_payment_method", "requires_confirmation", "requires_action")
        .contains(intent.getStatus())) {
      throw new ApiException(
          HttpStatus.CONFLICT, "PAYMENT_NOT_PAYABLE", "Payment is processing or already completed");
    }
    if (intent.getClientSecret() == null || intent.getClientSecret().isBlank())
      throw invalidProvider();
    // No client secret is exposed until Order durably binds this exact intent.
    orders.bind(
        BindPaymentIntentRequest.newBuilder()
            .setOrderId(orderId)
            .setPaymentIntentId(intent.getId())
            .setAmountMinor(order.getTotalAmountMinor())
            .setCurrency(currency)
            .build());
    return new IntentResponse(
        intent.getId(), intent.getClientSecret(), order.getTotalAmountMinor(), currency);
  }

  void validateIntent(PaymentIntent intent) {
    if (intent == null
        || intent.getId() == null
        || !intent.getId().matches("pi_[A-Za-z0-9]+")
        || !Boolean.valueOf(liveMode).equals(intent.getLivemode())
        || intent.getAmount() == null
        || intent.getAmount() <= 0
        || intent.getCurrency() == null
        || !intent.getCurrency().matches("[a-zA-Z]{3}")
        || intent.getMetadata() == null
        || !"gelatoflow-v1".equals(intent.getMetadata().get("integration"))
        || !Ids.isUuid(intent.getMetadata().get("order_id"))
        || !Ids.isUuid(intent.getMetadata().get("customer_id"))) {
      throw invalidProvider();
    }
  }

  private ApiException invalidOrder() {
    return new ApiException(
        HttpStatus.BAD_GATEWAY,
        "ORDER_INVALID_RESPONSE",
        "Order returned invalid checkout details");
  }

  private ApiException invalidProvider() {
    return new ApiException(
        HttpStatus.BAD_GATEWAY,
        "PAYMENT_INVALID_RESPONSE",
        "Provider payment could not be reconciled");
  }
}
