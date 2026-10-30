# Panduan kode ReserveFlow

Dokumen ini menjelaskan alur dari request HTTP sampai perubahan database. Baca sambil membuka file yang disebutkan. Data Alpha/Beta adalah data contoh untuk latihan, bukan pelanggan asli.

## 1. Program mulai dari `cmd/api/main.go`

`main()` memeriksa argumen `health`. Pada mode tersebut, program hanya memanggil `/healthz` dengan timeout lalu mengembalikan exit code. Docker memakai mode ini untuk health check; mode ini tidak menjalankan server kedua.

`run()` membaca dua digest API key lewat `digest()`. Digest berupa 32 byte SHA-256 yang ditulis sebagai 64 karakter heksadesimal. Nilai kunci mentah tidak dibutuhkan proses API. Dua tenant tidak boleh memiliki digest yang sama.

Program membaca `DATABASE_URL`, membuat connection pool maksimum 16 koneksi, lalu melakukan Ping. Selanjutnya ia membuka transaksi, mengambil advisory lock dan mengeksekusi schema yang di-embed. Lock mencegah dua instance awal membuat tabel bersamaan. `CREATE TABLE IF NOT EXISTS` adalah inisialisasi awal; perubahan schema berikutnya tetap memerlukan strategi migrasi tersendiri.

`http.Server` membatasi waktu membaca header, body dan menulis respons. Goroutine shutdown menunggu SIGINT/SIGTERM lalu memberi request yang sedang berjalan waktu untuk selesai. Pesan kegagalan startup sengaja tidak mencetak DSN agar password database tidak masuk log.

## 2. Kontrak data di `internal/api/model.go`

`Product` menjadi bentuk JSON produk. Field Go `OnHand` tampil sebagai `onHand` karena tag JSON. `Reserved` dan `Available` dihitung dari keadaan reservasi, bukan diterima dari client.

`Reservation` menyimpan jumlah, status dan waktu. Status tersimpan adalah `held`, `confirmed` atau `cancelled`. `effective()` mengubah salinan hasil baca menjadi `expired` bila waktu habis. Fungsi ini tidak menulis database.

`productInput` memakai `*int` untuk `onHand`. Pointer membedakan stok nol yang sah dari field hilang atau `null`. Setelah validasi handler berhasil, fungsi penyimpanan boleh mengambil `*in.OnHand`.

`page[T]` memakai generics Go. Bentuk halaman sama untuk produk, reservasi dan audit, tetapi tipe item tetap diperiksa compiler.

`encoded()` mengubah nilai menjadi byte JSON sekali. Byte inilah yang dikirim dan, untuk reservation write, disimpan dalam tabel idempotency. `problem()` memakai envelope error yang sama pada semua error aplikasi.

`makeCursor()` menyimpan resource, tenant dan ID terakhir dalam Base64 URL-safe. `readCursor()` memvalidasi ketiganya. Cursor bukan token autentikasi; filter SQL tenant tetap menentukan data yang boleh dibaca.

## 3. HTTP di `internal/api/http.go`

`Handler()` mendaftarkan health, OpenAPI dan kelompok `/v1/`. Middleware memberi deadline context 10 detik, `Cache-Control: no-store` dan `X-Content-Type-Options: nosniff`.

`auth()` mengambil Bearer key, menghitung SHA-256 lalu membandingkan digest dengan `crypto/subtle`. Hasil cocok menentukan tenant yang disimpan pada context. Dengan pola ini, JSON `tenant` tidak pernah dipakai untuk memberi hak akses.

`decode()` memastikan media type JSON, membatasi body menjadi 4096 byte, menolak top-level selain object, memakai `DisallowUnknownFields`, lalu memastikan tidak ada objek kedua. Perhatikan urutannya: body dibatasi sebelum seluruh data dibaca.

`dispatch()` memilih endpoint. Fungsi ini memvalidasi field bisnis dan `Idempotency-Key` sebelum memanggil penyimpanan. Misalnya, TTL nol ditolak sebagai 400, sehingga tidak membuat transaksi idempotency. `pageArgs()` membatasi jumlah halaman dan menolak query tak dikenal atau berulang.

`write()` mengirim content type, status dan byte respons. Pada replay ia menambahkan header `Idempotency-Replayed: true`; pada 503 ia menambahkan `Retry-After: 1`. Header replay boleh berubah, tetapi body/status asli dipertahankan.

## 4. Database di `internal/api/store.go`

`Server` membawa pool, digest, fungsi jam dan failpoint internal. Hanya `New()` dan `Handler()` yang diperlukan program utama. Test dalam package yang sama dapat mengganti jam/failpoint tanpa membuat endpoint pengujian publik.

`createProduct()` membuka transaksi, menulis produk dan audit, lalu commit. Konflik unique SKU dipetakan menjadi 409. Produk tidak memakai replay idempotency; uniqueness SKU membantu client menemukan hasil saat respons jaringan hilang.

`productSelect` menggunakan subquery SUM untuk jumlah hold yang masih berlaku. `scanProduct()` mengisi struct lalu menghitung `Available = OnHand - Reserved`. `products()` meminta `limit+1` baris untuk menentukan cursor berikutnya, tanpa query COUNT seluruh tabel.

`reservation()` dan `reservations()` memanggil `effective()` saat membaca. Karena itu hold yang kedaluwarsa tidak memerlukan background job sebelum dapat ditampilkan sebagai expired. `events()` membaca audit dengan pagination yang sama.

### Fungsi `mutate()`

Ini pembungkus transaksi untuk semua perubahan reservasi:

1. Serialisasi struct request yang sudah divalidasi. Gabungkan route dengan JSON, lalu hitung fingerprint SHA-256.
2. Mulai transaksi. Coba insert idempotency key dengan `ON CONFLICT DO NOTHING`.
3. Ambil baris key dengan `FOR UPDATE`. Request identik yang datang bersamaan akan menunggu.
4. Bila fingerprint berbeda, kembalikan 409. Bila respons sudah tersedia, kembalikan byte lama sebagai replay.
5. Jalankan callback perubahan stok/reservasi. Callback dapat menghasilkan business error tanpa error database; hasil tersebut tetap disimpan.
6. Simpan status dan body, lalu commit. `defer tx.Rollback(ctx)` membersihkan transaksi pada semua jalur yang keluar lebih awal; setelah commit, rollback tidak mengubah data.

Failpoint dipanggil di antara tahap penulisan dan sebelum commit untuk membuktikan rollback saat test. Tidak ada environment variable atau route untuk mengaktifkannya pada server biasa.

### Fungsi `reserve()`

Callback mengunci produk milik tenant. Sesudah lock berhasil, ia mengambil waktu dan menjumlah hold aktif. Bila permintaan melebihi `onHand - reserved`, hasilnya 409. Bila cukup, ia menulis reservasi dan audit. Tabel produk belum dikurangi, karena ini masih hold.

Misalnya stok satu, lalu 24 request datang. Request pertama memperoleh lock dan menulis satu hold. Request berikutnya baru dapat memeriksa kapasitas setelah commit pertama, sehingga melihat reserved satu dan available nol.

### Fungsi `transition()`

Fungsi ini membaca product ID dari reservasi, mengunci produk, lalu mengunci ulang reservasi. Product ID tidak dapat diubah endpoint mana pun. Urutan product-before-reservation dipakai pada confirm dan cancel.

Waktu diperiksa setelah product lock. Hold yang sudah habis mengembalikan 410. Status terminal mengembalikan 409. Confirm mengurangi `on_hand` lalu mengubah status; cancel hanya mengubah status. Keduanya menulis audit dan respons idempotent dalam transaksi yang sama.

## 5. Constraint di `internal/api/schema.sql`

- Unique `(tenant,sku)` mengizinkan dua tenant memakai SKU sama sambil menolak duplikasi dalam tenant yang sama.
- Foreign key `(tenant,product_id)` mencegah referensi produk lintas tenant.
- CHECK menjaga stok nonnegatif, jumlah positif dan status tersimpan yang dikenal.
- Partial index hold aktif memusatkan pencarian pada baris `held`; batas waktu tetap menjadi kondisi query karena waktu sekarang tidak cocok sebagai partial-index predicate yang berubah sendiri.
- Index `(tenant,id)` mendukung pagination berurutan.

Constraint tidak sendirian menjamin jumlah hold antarbaris. Protokol row lock pada semua perubahan reservasi memenuhi bagian tersebut. Query manual langsung ke database dapat melanggar protokol aplikasi.

## 6. Pengujian di `internal/api/api_test.go`

`database()` hanya menerima database bernama `reserveflow_test`. Setiap test membuat schema acak dan mengatur `search_path` pool sendiri. Cleanup menutup server/pool lalu menghapus schema tersebut. Test tidak memakai database utama.

`httptest.NewServer` menjalankan handler nyata. Helper `request()` mengirim HTTP dan membaca body sebenarnya. Test konkurensi memakai goroutine dan start barrier agar request dapat bertarung pada PostgreSQL, bukan mock repository.

Jam memakai `atomic.Int64` berisi Unix microseconds. Test expiry memindahkan waktu tepat ke `expiresAt`; tidak perlu `Sleep`. Failpoint memakai atomic flag supaya perubahan saat HTTP server berjalan aman dibaca.

Skenario memeriksa jumlah efek, status akhir, stok, body replay dan audit. Kode test bukan bukti sudah lulus: lihat [VERIFICATION.md](VERIFICATION.md) dan hasil workflow.

## 7. Docker dan skrip

`Dockerfile` mengompilasi binary API dan binary test dalam satu builder. Target `binaries` mengekspor kedua file Linux untuk verifikasi terpisah. Target runtime memakai user 10001; Compose menambahkan filesystem read-only dan menghapus capabilities.

`scripts/init.mjs` membuat random 32-byte key, menulis raw key hanya ke berkas lokal yang diabaikan Git, dan menurunkan digest SHA-256 untuk konfigurasi API. Kunci yang sudah ada divalidasi, tidak diam-diam diganti.

`Start.ps1`, `Stop.ps1` dan `Test.ps1` selalu menyebut file serta nama project Compose. Test memakai database tmpfs sendiri. `smoke.mjs` menulis bukti status HTTP tanpa menulis kunci ke laporan.

## Urutan belajar

Jalankan demo create product, hold, replay, confirm dan GET stok. Lalu buka `dispatch()`, `mutate()`, `reserve()` dan `transition()` dengan urutan tersebut. Sesudah alur request terbaca, pelajari schema dan test konkurensi. Dengan begitu setiap detail Go dan SQL terhubung ke perubahan data yang sudah kamu lihat.
