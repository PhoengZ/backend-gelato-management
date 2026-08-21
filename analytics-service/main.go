package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
)

func main() {
	app := fiber.New()

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendString("Analytics Service is up and running!")
	})

	log.Fatal(app.Listen(":3000"))
}
