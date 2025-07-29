package upload

import (
	"path/filepath"

	"github.com/gofiber/fiber/v2"
)

func Upload(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Failed to upload file"})
	}

	name := c.FormValue("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "File name required"})
	}

	dst := filepath.Join("public/images", name)
	if err := c.SaveFile(file, dst); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to save file"})
	}

	return c.JSON(fiber.Map{"message": "File Uploaded Successfully"})
}

func Setup(api fiber.Router) {
	api.Post("/upload", Upload)
}