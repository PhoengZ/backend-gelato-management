package com.gelatoflow.payment.client.order;

public interface OrderCallbackPort {
  void succeeded(String orderId);

  void failed(String orderId);
}
