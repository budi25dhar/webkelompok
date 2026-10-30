package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

const maxUploadSize int64 = 10 << 20 // 10 MiB

// User is the API representation of a row in the anggota table.
// Password hashes are deliberately never returned in API responses.
type User struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	Code         string `json:"code"`
	FullName     string `json:"nama_lengkap"`
	PlaceOfBirth string `json:"tempat_lahir"`
	DateOfBirth  string `json:"tanggal_lahir"`
	Major        string `json:"jurusan"`
	Division     string `json:"divisi"`
}

type Photo struct {
	ID     uint64 `json:"id"`
	Code   string `json:"code"`
	Link   string `json:"link_foto"`
	FileID string `json:"id_file"`
}

type ImageKitResponse struct {
	FileID string `json:"fileId"`
	URL    string `json:"url"`
}

func main() {
	// .env is optional if the environment variables are already configured.
	if err := godotenv.Load(".env"); err != nil {
		log.Println(".env not found; using environment variables")
	}

	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")
	if dbUser == "" || dbHost == "" || dbPort == "" || dbName == "" {
		log.Fatal("DB_USER, DB_HOST, DB_PORT, and DB_NAME must be configured in .env")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&loc=Local", dbUser, dbPassword, dbHost, dbPort, dbName)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal("Could not open database: ", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatal("MySQL connection failed: ", err)
	}
	log.Println("Connected to MySQL database:", dbName)

	routes(db)
}

func routes(db *sql.DB) {
	r := gin.Default()

	// Development CORS middleware. For production, restrict allowed origins.
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", os.Getenv("FRONTEND_ORIGIN"))
		if os.Getenv("FRONTEND_ORIGIN") == "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// Keep the mentor's /users API paths to minimize frontend changes.
	r.POST("/users", func(c *gin.Context) { createUser(c, db) })
	r.GET("/users", func(c *gin.Context) { getUsers(c, db) })
	r.GET("/users/:code", func(c *gin.Context) { getUser(c, db) })
	r.PUT("/users/:code", func(c *gin.Context) { updateUser(c, db) })
	r.DELETE("/users/:code", func(c *gin.Context) { deleteUser(c, db) })
	r.POST("/login", func(c *gin.Context) { login(c, db) })

	r.POST("/users/:code/photos", func(c *gin.Context) { uploadPhoto(c, db) })
	r.GET("/users/:code/photos", func(c *gin.Context) { getPhotos(c, db) })

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Backend listening on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal("Server stopped: ", err)
	}
}

// ------------------------------ User handlers ------------------------------

func createUser(c *gin.Context, db *sql.DB) {
	var input struct {
		Name         string `json:"name"`
		Code         string `json:"code"`
		Password     string `json:"password"`
		FullName     string `json:"nama_lengkap"`
		PlaceOfBirth string `json:"tempat_lahir"`
		DateOfBirth  string `json:"tanggal_lahir"`
		Major        string `json:"jurusan"`
		Division     string `json:"divisi"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON tidak valid"})
		return
	}

	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.FullName = strings.TrimSpace(input.FullName)
	input.PlaceOfBirth = strings.TrimSpace(input.PlaceOfBirth)
	input.Major = strings.TrimSpace(input.Major)
	input.Division = strings.ToUpper(strings.TrimSpace(input.Division))

	if input.Name == "" || input.Code == "" || input.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, code, dan password wajib diisi"})
		return
	}
	if len(input.Code) != 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code harus tepat 3 karakter"})
		return
	}
	if len(input.Division) > 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "divisi maksimal 3 karakter"})
		return
	}
	if len([]byte(input.Password)) > 72 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password maksimal 72 byte"})
		return
	}
	if _, err := time.Parse("2006-01-02", input.DateOfBirth); input.DateOfBirth != "" && err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tanggal_lahir harus berformat YYYY-MM-DD"})
		return
	}
	if input.DateOfBirth == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tanggal_lahir wajib diisi karena kolom database tidak menerima NULL"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memproses password"})
		return
	}

	result, err := db.Exec(`INSERT INTO anggota
		(nama, kode, password, nama_lengkap, tempat_lahir, tanggal_lahir, jurusan, divisi)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Name, input.Code, string(hash), input.FullName, input.PlaceOfBirth,
		input.DateOfBirth, input.Major, input.Division)
	if err != nil {
		if isDuplicateKey(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "kode anggota sudah digunakan"})
			return
		}
		log.Printf("Create anggota failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menambahkan anggota; periksa struktur database dan panjang kolom password"})
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Anggota dibuat, tetapi ID tidak dapat dibaca"})
		return
	}
	c.JSON(http.StatusCreated, User{ID: uint64(id), Name: input.Name, Code: input.Code,
		FullName: input.FullName, PlaceOfBirth: input.PlaceOfBirth,
		DateOfBirth: input.DateOfBirth, Major: input.Major, Division: input.Division})
}

func getUsers(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`SELECT id, nama, kode, nama_lengkap, tempat_lahir,
		tanggal_lahir, jurusan, divisi FROM anggota ORDER BY id`)
	if err != nil {
		log.Printf("Get anggota failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengambil data anggota"})
		return
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		var u User
		var dob time.Time
		if err := rows.Scan(&u.ID, &u.Name, &u.Code, &u.FullName, &u.PlaceOfBirth, &dob, &u.Major, &u.Division); err != nil {
			log.Printf("Scan anggota failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membaca data anggota"})
			return
		}
		u.DateOfBirth = dob.Format("2006-01-02")
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Terjadi kesalahan saat membaca database"})
		return
	}
	c.JSON(http.StatusOK, users)
}

func getUser(c *gin.Context, db *sql.DB) {
	var u User
	var dob time.Time
	err := db.QueryRow(`SELECT id, nama, kode, nama_lengkap, tempat_lahir,
		tanggal_lahir, jurusan, divisi FROM anggota WHERE kode = ?`,
		strings.ToUpper(strings.TrimSpace(c.Param("code")))).Scan(
		&u.ID, &u.Name, &u.Code, &u.FullName, &u.PlaceOfBirth, &dob, &u.Major, &u.Division)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Anggota tidak ditemukan"})
		return
	}
	if err != nil {
		log.Printf("Get anggota failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengambil data anggota"})
		return
	}
	u.DateOfBirth = dob.Format("2006-01-02")
	c.JSON(http.StatusOK, u)
}

func updateUser(c *gin.Context, db *sql.DB) {
	var input struct {
		Name         string `json:"name"`
		Password     string `json:"password"`
		FullName     string `json:"nama_lengkap"`
		PlaceOfBirth string `json:"tempat_lahir"`
		DateOfBirth  string `json:"tanggal_lahir"`
		Major        string `json:"jurusan"`
		Division     string `json:"divisi"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON tidak valid"})
		return
	}
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	if input.Name == "" || input.DateOfBirth == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name dan tanggal_lahir wajib diisi"})
		return
	}
	if _, err := time.Parse("2006-01-02", input.DateOfBirth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tanggal_lahir harus berformat YYYY-MM-DD"})
		return
	}
	if len(input.Division) > 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "divisi maksimal 3 karakter"})
		return
	}

	var result sql.Result
	var err error
	if input.Password != "" {
		if len([]byte(input.Password)) > 72 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password maksimal 72 byte"})
			return
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memproses password"})
			return
		}
		result, err = db.Exec(`UPDATE anggota SET nama=?, password=?, nama_lengkap=?, tempat_lahir=?, tanggal_lahir=?, jurusan=?, divisi=? WHERE kode=?`,
			strings.TrimSpace(input.Name), string(hash), strings.TrimSpace(input.FullName), strings.TrimSpace(input.PlaceOfBirth), input.DateOfBirth,
			strings.TrimSpace(input.Major), strings.ToUpper(strings.TrimSpace(input.Division)), code)
	} else {
		result, err = db.Exec(`UPDATE anggota SET nama=?, nama_lengkap=?, tempat_lahir=?, tanggal_lahir=?, jurusan=?, divisi=? WHERE kode=?`,
			strings.TrimSpace(input.Name), strings.TrimSpace(input.FullName), strings.TrimSpace(input.PlaceOfBirth), input.DateOfBirth,
			strings.TrimSpace(input.Major), strings.ToUpper(strings.TrimSpace(input.Division)), code)
	}
	if err != nil {
		log.Printf("Update anggota failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memperbarui anggota; periksa panjang kolom password jika password diubah"})
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tidak dapat memverifikasi perubahan"})
		return
	}
	if n == 0 {
		var exists int
		err := db.QueryRow("SELECT 1 FROM anggota WHERE kode=?", code).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Anggota tidak ditemukan"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memeriksa anggota"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Data anggota berhasil diperbarui"})
}

func deleteUser(c *gin.Context, db *sql.DB) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memulai transaksi database"})
		return
	}
	// Delete related photos first in case a foreign key prevents deleting anggota.
	if _, err = tx.Exec("DELETE FROM foto_anggota WHERE kode=?", code); err != nil {
		_ = tx.Rollback()
		log.Printf("Delete photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus foto anggota"})
		return
	}
	result, err := tx.Exec("DELETE FROM anggota WHERE kode=?", code)
	if err != nil {
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus anggota"})
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tidak dapat memverifikasi penghapusan"})
		return
	}
	if n == 0 {
		_ = tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "Anggota tidak ditemukan"})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyelesaikan penghapusan"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Anggota dan data fotonya berhasil dihapus"})
}

func login(c *gin.Context, db *sql.DB) {
	var input struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON tidak valid"})
		return
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	if input.Code == "" || input.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code dan password wajib diisi"})
		return
	}
	var u User
	var hash string
	var dob time.Time
	err := db.QueryRow(`SELECT id, nama, kode, password, nama_lengkap, tempat_lahir,
		tanggal_lahir, jurusan, divisi FROM anggota WHERE kode=?`, input.Code).Scan(
		&u.ID, &u.Name, &u.Code, &hash, &u.FullName, &u.PlaceOfBirth, &dob, &u.Major, &u.Division)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Kode anggota atau password salah"})
		return
	}
	if err != nil {
		log.Printf("Login query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memproses login"})
		return
	}
	u.DateOfBirth = dob.Format("2006-01-02")
	c.JSON(http.StatusOK, gin.H{"message": "Login berhasil", "user": u})
}

func isDuplicateKey(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// ------------------------------ Photo handlers -----------------------------

func uploadPhoto(c *gin.Context, db *sql.DB) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	var exists int
	err := db.QueryRow("SELECT 1 FROM anggota WHERE kode=?", code).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Anggota tidak ditemukan"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memeriksa anggota"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Kirim file gambar melalui field multipart 'file' dengan ukuran maksimal 10 MiB"})
		return
	}
	defer file.Close()
	if header.Size > maxUploadSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ukuran file maksimal 10 MiB"})
		return
	}

	privateKey := os.Getenv("IMAGEKIT_PRIVATE_KEY")
	if privateKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "IMAGEKIT_PRIVATE_KEY belum dikonfigurasi"})
		return
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyiapkan file gambar"})
		return
	}
	if _, err := io.Copy(part, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membaca file gambar"})
		return
	}
	if err := writer.WriteField("fileName", header.Filename); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyiapkan nama file"})
		return
	}
	if err := writer.WriteField("useUniqueFileName", "true"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyiapkan metadata file"})
		return
	}
	if err := writer.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyiapkan upload"})
		return
	}

	req, err := http.NewRequest(http.MethodPost, "https://upload.imagekit.io/api/v1/files/upload", &body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membuat request upload"})
		return
	}
	req.SetBasicAuth(privateKey, "")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Upload ke ImageKit gagal"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("ImageKit returned status %d: %s", resp.StatusCode, string(responseBody))
		c.JSON(http.StatusBadGateway, gin.H{"error": "ImageKit menolak upload; periksa private key dan konfigurasi ImageKit"})
		return
	}
	var ik ImageKitResponse
	if err := json.NewDecoder(resp.Body).Decode(&ik); err != nil || ik.URL == "" || ik.FileID == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Respons ImageKit tidak lengkap"})
		return
	}

	result, err := db.Exec("INSERT INTO foto_anggota (kode, link_foto, id_file) VALUES (?, ?, ?)", code, ik.URL, ik.FileID)
	if err != nil {
		log.Printf("Image uploaded but DB save failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Foto terunggah, tetapi gagal disimpan ke database"})
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Foto tersimpan, tetapi ID tidak dapat dibaca"})
		return
	}
	c.JSON(http.StatusCreated, Photo{ID: uint64(id), Code: code, Link: ik.URL, FileID: ik.FileID})
}

func getPhotos(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`SELECT id, kode, link_foto, id_file
		FROM foto_anggota WHERE kode=? ORDER BY id DESC`,
		strings.ToUpper(strings.TrimSpace(c.Param("code"))))
	if err != nil {
		log.Printf("Get photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengambil foto anggota"})
		return
	}
	defer rows.Close()
	photos := []Photo{}
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.ID, &p.Code, &p.Link, &p.FileID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membaca data foto"})
			return
		}
		photos = append(photos, p)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Terjadi kesalahan saat membaca database"})
		return
	}
	c.JSON(http.StatusOK, photos)
}
