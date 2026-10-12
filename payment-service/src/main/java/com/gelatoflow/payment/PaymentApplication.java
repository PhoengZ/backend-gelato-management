package com.gelatoflow.payment;

import com.gelatoflow.payment.config.CheckoutProperties;
import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.config.PaymentProperties;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.context.properties.EnableConfigurationProperties;

@SpringBootApplication
@EnableConfigurationProperties({
  PaymentProperties.class,
  CheckoutProperties.class,
  OrderCallbackProperties.class
})
public class PaymentApplication {
  public static void main(String[] args) {
    SpringApplication.run(PaymentApplication.class, args);
  }
}
