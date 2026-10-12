package com.gelatoflow.payment.dto;

import com.fasterxml.jackson.annotation.JsonProperty;

public record PaymentIntentResponse(
    @JsonProperty("payment_intent_id") String paymentIntentId,
    @JsonProperty("client_secret") String clientSecret,
    @JsonProperty("amount_minor") long amountMinor,
    String currency) {}
