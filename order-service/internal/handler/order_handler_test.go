package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"order-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

type callbackService struct {
	service.Service
	confirms, cancels int
}

func (s *callbackService) ConfirmPayment(context.Context, string) error {
	s.confirms++
	return nil
}

func (s *callbackService) CancelOrder(context.Context, string, string) error {
	s.cancels++
	return nil
}

func TestPaymentCallbacksRequireServiceTokenBeforeBusinessAction(t *testing.T) {
	const validToken = "local_payment_callback_test_token_32_bytes"
	for _, path := range []string{"payment-succeeded", "payment-failed"} {
		for _, token := range []string{"", "wrong", "local_payment_callback_test_token_32_byteX", validToken} {
			name := "missing"
			if token != "" {
				name = "invalid"
			}
			if token == validToken {
				name = "valid"
			}
			t.Run(path+"/"+name, func(t *testing.T) {
				svc := &callbackService{}
				h, err := NewOrderHandler(svc, validToken)
				if err != nil {
					t.Fatal(err)
				}
				app := fiber.New()
				app.Post("/payment-succeeded", h.PaymentSucceeded)
				app.Post("/payment-failed", h.PaymentFailed)
				request := httptest.NewRequest(http.MethodPost, "/"+path, nil)
				if token != "" {
					request.Header.Set("X-Payment-Service-Token", token)
				}
				response, err := app.Test(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if token != validToken {
					if response.StatusCode != http.StatusUnauthorized {
						t.Errorf("expected 401, got %d", response.StatusCode)
					}
					if svc.confirms != 0 || svc.cancels != 0 {
						t.Error("unauthenticated callback performed a business action")
					}
					var body struct {
						Code string `json:"code"`
					}
					if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body.Code != "UNAUTHENTICATED" {
						t.Errorf("unexpected error code %q", body.Code)
					}
					return
				}
				if response.StatusCode != http.StatusNoContent || svc.confirms+svc.cancels != 1 {
					t.Fatal("valid callback did not perform exactly one business action")
				}
			})
		}
	}
}
