package com.gelatoflow.payment.controller;

import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.gelatoflow.payment.exception.ApiException;
import com.gelatoflow.payment.testsupport.StripeWebhookFixtures;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.MACSigner;
import com.nimbusds.jwt.*;
import java.time.Instant;
import java.util.Date;
import org.junit.jupiter.api.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
    properties = {
      "payment.stripe.secret-key=sk_test_fixture",
          "payment.stripe.webhook-secret=whsec_test_fixture",
      "payment.order.service-token=fixture", "payment.order.tls=false",
      "payment.jwt.secret=test-signing-secret-at-least-32-bytes"
    })
@AutoConfigureMockMvc
class PaymentHttpTest {
  private static final String CREATE = "/api/v1/payments/create-payment-intent";
  @Autowired MockMvc mvc;
  @MockitoBean OrderPort orders;
  @MockitoBean StripePort stripe;

  @BeforeEach
  void setup() {
    when(orders.get(ORDER)).thenReturn(order());
    when(stripe.create(ORDER, CUSTOMER, 25000, "THB"))
        .thenReturn(intent("requires_payment_method"));
  }

  String token(String role, String issuer, String audience, String subject, Instant expiry)
      throws Exception {
    var jwt =
        new SignedJWT(
            new JWSHeader(JWSAlgorithm.HS256),
            new JWTClaimsSet.Builder()
                .subject(subject)
                .issuer(issuer)
                .audience(audience)
                .claim("role", role)
                .issueTime(Date.from(Instant.now().minusSeconds(10)))
                .expirationTime(Date.from(expiry))
                .build());
    jwt.sign(new MACSigner(SECRET));
    return "Bearer " + jwt.serialize();
  }

  String customer() throws Exception {
    return token(
        "CUSTOMER", "gelatoflow-auth", "gelatoflow-api", CUSTOMER, Instant.now().plusSeconds(300));
  }

  @Test
  void customerReceivesCanonicalResponseWithNoCache() throws Exception {
    mvc.perform(
            post(CREATE)
                .header("Authorization", customer())
                .contentType("application/json")
                .content("{\"order_id\":\"" + ORDER + "\"}"))
        .andExpect(status().isCreated())
        .andExpect(header().string("Cache-Control", "no-store"))
        .andExpect(jsonPath("$.client_secret").value("pi_fixture_secret_fixture"))
        .andExpect(jsonPath("$.amount_minor").value(25000))
        .andExpect(jsonPath("$.currency").value("THB"));
  }

  @Test
  void missingTokenAndSpoofedIdentityHeadersFailClosed() throws Exception {
    mvc.perform(
            post(CREATE)
                .header("X-User-Id", CUSTOMER)
                .header("X-User-Role", "CUSTOMER")
                .contentType("application/json")
                .content("{\"order_id\":\"" + ORDER + "\"}"))
        .andExpect(status().isUnauthorized())
        .andExpect(jsonPath("$.code").value("UNAUTHORIZED"));
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void incorrectJwtClaimsAndExpiredTokensAreRejected() throws Exception {
    var validExpiry = Instant.now().plusSeconds(300);
    for (String auth :
        new String[] {
          token("CUSTOMER", "wrong", "gelatoflow-api", CUSTOMER, validExpiry),
          token("CUSTOMER", "gelatoflow-auth", "wrong", CUSTOMER, validExpiry),
          token("CUSTOMER", "gelatoflow-auth", "gelatoflow-api", "invalid", validExpiry),
          token(
              "CUSTOMER",
              "gelatoflow-auth",
              "gelatoflow-api",
              CUSTOMER,
              Instant.now().minusSeconds(120))
        }) {
      mvc.perform(
              post(CREATE)
                  .header("Authorization", auth)
                  .contentType("application/json")
                  .content("{\"order_id\":\"" + ORDER + "\"}"))
          .andExpect(status().isUnauthorized());
    }
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void staffCannotUseCustomerCheckout() throws Exception {
    mvc.perform(
            post(CREATE)
                .header(
                    "Authorization",
                    token(
                        "STAFF",
                        "gelatoflow-auth",
                        "gelatoflow-api",
                        CUSTOMER,
                        Instant.now().plusSeconds(300)))
                .contentType("application/json")
                .content("{\"order_id\":\"" + ORDER + "\"}"))
        .andExpect(status().isForbidden())
        .andExpect(jsonPath("$.code").value("FORBIDDEN"));
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void missingRequiredClaimsAndWrongSignatureAreAuthenticationFailures() throws Exception {
    var complete =
        new JWTClaimsSet.Builder()
            .subject(CUSTOMER)
            .issuer("gelatoflow-auth")
            .audience("gelatoflow-api")
            .claim("role", "CUSTOMER")
            .issueTime(Date.from(Instant.now().minusSeconds(10)))
            .expirationTime(Date.from(Instant.now().plusSeconds(300)))
            .build();
    for (String claim : new String[] {"aud", "role", "exp", "iat"}) {
      var incomplete = new java.util.HashMap<>(complete.toJSONObject());
      incomplete.remove(claim);
      var jwt = new SignedJWT(new JWSHeader(JWSAlgorithm.HS256), JWTClaimsSet.parse(incomplete));
      jwt.sign(new MACSigner(SECRET));
      mvc.perform(
              post(CREATE)
                  .header("Authorization", "Bearer " + jwt.serialize())
                  .contentType("application/json")
                  .content("{\"order_id\":\"" + ORDER + "\"}"))
          .andExpect(status().isUnauthorized());
    }
    var forged = new SignedJWT(new JWSHeader(JWSAlgorithm.HS256), complete);
    forged.sign(new MACSigner("wrong-signing-secret-at-least-32-bytes"));
    mvc.perform(
            post(CREATE)
                .header("Authorization", "Bearer " + forged.serialize())
                .contentType("application/json")
                .content("{\"order_id\":\"" + ORDER + "\"}"))
        .andExpect(status().isUnauthorized());
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void webhookDoesNotAcknowledgeResultWhenOrderIsUnavailable() throws Exception {
    when(stripe.retrieve("pi_fixture")).thenReturn(intent("succeeded"));
    doThrow(
            new ApiException(
                org.springframework.http.HttpStatus.SERVICE_UNAVAILABLE,
                "ORDER_UNAVAILABLE",
                "retry delivery"))
        .when(orders)
        .report(any());
    var body = StripeWebhookFixtures.payload("payment_intent.succeeded");
    mvc.perform(
            post("/api/v1/payments/webhook")
                .contentType("application/json")
                .header(
                    "Stripe-Signature",
                    StripeWebhookFixtures.sign(body, Instant.now().getEpochSecond()))
                .content(body))
        .andExpect(status().isServiceUnavailable())
        .andExpect(jsonPath("$.code").value("ORDER_UNAVAILABLE"));
  }

  @Test
  void rejectsLegacyClientAmountUnknownFieldsMalformedJsonAndInvalidId() throws Exception {
    for (String body :
        new String[] {
          "{\"amount\":250}",
          "{\"order_id\":\"" + ORDER + "\",\"amount_minor\":1}",
          "{",
          "{}",
          "{\"order_id\":\"bad\"}"
        }) {
      mvc.perform(
              post(CREATE)
                  .header("Authorization", customer())
                  .contentType("application/json")
                  .content(body))
          .andExpect(status().isBadRequest())
          .andExpect(jsonPath("$.code").exists());
    }
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void webhookRequiresSignatureButNoUserJwtAndPreservesRawBody() throws Exception {
    var body = StripeWebhookFixtures.payload("customer.created");
    mvc.perform(post("/api/v1/payments/webhook").contentType("application/json").content(body))
        .andExpect(status().isBadRequest())
        .andExpect(jsonPath("$.code").value("INVALID_WEBHOOK"));
    mvc.perform(
            post("/api/v1/payments/webhook")
                .contentType("application/json")
                .header(
                    "Stripe-Signature",
                    StripeWebhookFixtures.sign(body, Instant.now().getEpochSecond()))
                .content(body))
        .andExpect(status().isOk())
        .andExpect(jsonPath("$.received").value(true));
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void limitsWebhookBodyAndProvidesPublicHealth() throws Exception {
    mvc.perform(
            post("/api/v1/payments/webhook")
                .contentType("application/json")
                .content(" ".repeat(262145)))
        .andExpect(status().isPayloadTooLarge());
    mvc.perform(get("/actuator/health"))
        .andExpect(status().isOk())
        .andExpect(jsonPath("$.status").value("UP"));
  }
}
