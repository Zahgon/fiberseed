package books

import (
	"encoding/json"
	"errors"
	"fiberseed/database"
	"fiberseed/pkg"
	"net/http"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

type Book struct {
	database.DefaultModel
	Title  string `json:"title"`
	Author string `json:"author"`
	Rating int    `json:"rating"`
}

func GetBooks(w http.ResponseWriter, r *http.Request) error {
	db := database.DB
	var books []Book
	db.Find(&books)
	return pkg.JSON(w, http.StatusOK, books)
}

func GetBook(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	db := database.DB
	var book Book
	err := db.First(&book, id).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pkg.EntityNotFound("No book found")
	} else if err != nil {
		return pkg.Unexpected(err.Error())
	}

	return pkg.JSON(w, http.StatusOK, book)
}

func NewBook(w http.ResponseWriter, r *http.Request) error {
	db := database.DB
	book := new(Book)
	if err := json.NewDecoder(r.Body).Decode(book); err != nil {
		return pkg.BadRequest("Invalid params")
	}
	db.Create(&book)
	return pkg.JSON(w, http.StatusOK, book)
}

func UpdateBook(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	db := database.DB
	var book Book
	err := db.First(&book, id).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pkg.EntityNotFound("No book found")
	} else if err != nil {
		return pkg.Unexpected(err.Error())
	}

	updatedBook := new(Book)

	if err := json.NewDecoder(r.Body).Decode(updatedBook); err != nil {
		return pkg.BadRequest("Invalid params")
	}

	updatedBook = &Book{Title: updatedBook.Title, Author: updatedBook.Author, Rating: updatedBook.Rating}

	if err = db.Model(&book).Updates(updatedBook).Error; err != nil {
		return pkg.Unexpected(err.Error())
	}

	return pkg.Status(w, http.StatusNoContent)
}

func DeleteBook(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	db := database.DB

	var book Book
	err := db.First(&book, id).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pkg.EntityNotFound("No book found")
	} else if err != nil {
		return pkg.Unexpected(err.Error())
	}

	db.Delete(&book)
	return pkg.Status(w, http.StatusNoContent)
}
