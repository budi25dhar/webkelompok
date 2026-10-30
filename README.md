# Backend webkelompok (Golang + Gin + MySQL)

## Persyaratan
- Go 1.22 atau lebih baru
- MySQL dengan database `webkelompok`
- ImageKit private key untuk fitur upload foto

## Penting: kolom password
Backend menggunakan bcrypt. Hash bcrypt memiliki panjang 60 karakter, sedangkan struktur database awal menunjukkan `anggota.password` adalah `VARCHAR(50)`. Ubah kapasitas kolom sebelum membuat anggota atau login dengan password hash:

```sql
USE webkelompok;
ALTER TABLE anggota MODIFY password VARCHAR(255) NOT NULL;
```

Jika database sudah berisi password teks biasa, login bcrypt tidak akan cocok untuk data lama. Buat ulang password lewat backend atau lakukan migrasi yang aman; jangan menganggap password lama otomatis menjadi hash.

## Menjalankan
1. Salin `.env.example` menjadi `.env`.
2. Sesuaikan DB_USER, DB_PASSWORD, DB_HOST, DB_PORT, DB_NAME.
3. Isi `IMAGEKIT_PRIVATE_KEY` jika fitur upload foto akan digunakan.
4. Di folder proyek jalankan:

```bash
go mod tidy
go run .
```

Server default berjalan pada `http://localhost:8080`.

## Endpoint
- `POST /users` — membuat anggota
- `GET /users` — daftar anggota
- `GET /users/:code` — detail anggota
- `PUT /users/:code` — memperbarui data anggota; password opsional
- `DELETE /users/:code` — menghapus anggota dan data foto dari database
- `POST /login` — login menggunakan `code` dan `password`
- `POST /users/:code/photos` — upload foto; multipart field bernama `file`
- `GET /users/:code/photos` — daftar foto anggota

## Contoh payload POST /users
```json
{
  "name": "budi",
  "code": "BUD",
  "password": "ganti-password-ini",
  "nama_lengkap": "Budi Dharmanta",
  "tempat_lahir": "Samarinda",
  "tanggal_lahir": "2006-01-01",
  "jurusan": "Teknik Telekomunikasi",
  "divisi": "WEB"
}
```

## Catatan
- Endpoint tetap memakai `/users` agar kompatibel dengan struktur API mentor, tetapi query mengakses tabel `anggota`.
- `description` tidak dipertahankan karena tidak ada kolom yang sesuai di tabel database.
- Upload memerlukan ImageKit; URL dan `fileId` disimpan ke `foto_anggota.link_foto` dan `foto_anggota.id_file`.
- CORS `*` hanya untuk memudahkan pengujian lokal. Batasi `FRONTEND_ORIGIN` untuk deployment.
- Endpoint login ini memverifikasi kredensial dan mengembalikan data anggota, tetapi belum menerbitkan token/session. Tambahkan autentikasi token jika endpoint privat perlu dilindungi.
- melakukan cloning git
  gitclon (masukin link git ulpi)
- pastikan go lang sudah terinstall
- file .env.example diubah menjadi --> .env
- mengganti variabel
- melakukan set up go 
  "go get webklompok"
  "go run."