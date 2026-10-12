package com.gelatoflow.payment.client.order;

import com.gelatoflow.contracts.order.v1.*;

public interface OrderPort {
  GetOrderDetailsResponse get(String orderId);

  void bind(BindPaymentIntentRequest request);

  void report(ReportPaymentResultRequest request);
}
