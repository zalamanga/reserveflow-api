# Penjelasan interview

## Penjelasan singkat

"Saya membuat ReserveFlow, REST API reservasi stok menggunakan Go dan PostgreSQL. Fokusnya adalah request yang datang bersamaan, retry jaringan dan isolasi tenant. Stok ditahan sementara, lalu dapat dikonfirmasi atau dibatalkan. Perubahan stok, audit dan respons idempotency memakai satu transaksi."

Sesuaikan kalimat itu dengan pemahaman dan kontribusi yang benar-benar kamu kuasai. Jangan mengaku sudah menjalankan load test, menangani pelanggan nyata atau membangun sistem pembayaran jika belum dilakukan.

## Demo yang bisa ditunjukkan

1. Buat produk dengan stok satu.
2. Buat hold satu unit. GET produk menunjukkan available nol.
3. Kirim ulang request dengan key sama. Body tetap sama dan header replay muncul.
4. Kirim hold kedua memakai key baru. Hasilnya 409.
5. Konfirmasi hold pertama. onHand menjadi nol dan reserved kembali nol.
6. Pakai key tenant Beta untuk membaca ID Alpha. Hasilnya 404.
7. Buka test `TestLastUnitConcurrentRequests` untuk menjelaskan perlombaan request terhadap PostgreSQL.

Postman collection menyusun alur tersebut. Jangan menampilkan panel environment yang memperlihatkan raw API key ketika merekam demo.

## Mengapa Go?

Go menyediakan HTTP server, context deadline, JSON, cryptography dan test HTTP dalam standard library. `pgx` dipakai untuk PostgreSQL pool dan transaksi. Goroutine dipakai test untuk membuat request bersamaan. Ini tidak otomatis membuktikan API cepat; klaim throughput memerlukan pengukuran tersendiri.

## Mengapa row lock?

Jika dua request hanya membaca stok tanpa lock, keduanya bisa melihat stok satu sebelum menulis hold. Dengan lock produk, request kedua menunggu transaksi pertama. Sesudah lock didapat, query SUM berikutnya melihat hasil commit pertama dan menolak kapasitas yang sudah dipakai.

## Mengapa idempotency disimpan di database?

Jika hanya disimpan dalam memory, restart atau dua instance berbeda dapat menghilangkan pengetahuan tentang request sebelumnya. Unique key tenant dan transaksi PostgreSQL membuat pengulangan bersamaan mengacu pada satu hasil yang tersimpan.

Ini memberikan satu efek untuk key yang sama dalam scope database aplikasi, bukan klaim semua jaringan mengirim pesan exactly once. Respons COMMIT bisa hilang di jaringan, sehingga client tetap harus mengulang key yang sama.

## Bagaimana kalau waktu hold habis tanpa background job?

Waktu habis dihitung saat request berjalan. Query kapasitas hanya menghitung hold dengan expires_at lebih besar dari waktu sekarang. Baris lama tetap tersimpan tetapi tidak menghalangi stok. GET menampilkan expired melalui fungsi `effective()`. Tidak ada audit expiry otomatis, karena tidak ada transaksi yang terjadi ketika jam melewati batas.

## Bagaimana menjaga tenant?

API menentukan tenant dari API key. Semua SQL menyertakan tenant dan foreign key produk/reservasi juga membawa tenant. Client tidak mengirim tenant ID untuk mendapatkan hak akses. ID milik tenant lain diperlakukan sebagai tidak ditemukan.

## Bagaimana membuktikan rollback?

Failpoint internal test sengaja mengembalikan error setelah penulisan, sebelum commit. Test lalu memeriksa stok, reservasi, audit dan key tidak berubah sebagian. Failpoint tidak dapat dinyalakan dari HTTP atau environment server biasa.

## Bagian yang belum dibuat

Belum ada pembayaran, stok masuk, multi-gudang, manajemen kunci, rate limiter atau deployment publik. Record audit/idempotency belum memiliki retention job. Schema awal dijalankan otomatis, sementara upgrade schema produksi memerlukan migrasi berversi. Jelaskan batas ini bila interviewer menanyakan pengembangan berikutnya.
