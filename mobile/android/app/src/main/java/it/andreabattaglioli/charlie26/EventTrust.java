package it.andreabattaglioli.charlie26;

import android.content.Context;
import android.content.SharedPreferences;
import android.net.http.SslCertificate;
import android.util.Base64;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.URL;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.security.cert.Certificate;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;

import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;

/**
 * Fiducia nel certificato dell'evento.
 *
 * Il PC di regia usa una propria CA locale. Il QR porta l'impronta (sha256)
 * di quella CA: l'app scarica la CA dal server, controlla che l'impronta
 * combaci e da quel momento accetta solo certificati firmati da essa per
 * quell'indirizzo. Un altro dispositivo in rete non può spacciarsi per la regia.
 */
final class EventTrust {
    private static final String PREFS = "charlie26.fiducia";
    private final SharedPreferences prefs;

    EventTrust(Context ctx) {
        prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    /** host:porta di un indirizzo https. */
    static String key(String url) {
        try {
            URL u = new URL(url);
            int port = u.getPort() == -1 ? 443 : u.getPort();
            return u.getHost().toLowerCase() + ":" + port;
        } catch (Exception e) {
            return "";
        }
    }

    boolean known(String url) {
        return prefs.contains(key(url));
    }

    /**
     * Scarica la CA dall'evento e la salva se l'impronta corrisponde al QR.
     * Da chiamare fuori dal thread principale. Restituisce null se va bene,
     * altrimenti il messaggio d'errore da mostrare.
     */
    String learn(String url, String pin) {
        String k = key(url);
        if (k.isEmpty()) return "Indirizzo non valido.";
        try {
            URL base = new URL(url);
            URL caUrl = new URL("https", base.getHost(), base.getPort(), "/ca.crt");
            // Collegamento senza verifica: la CA scaricata viene poi controllata con l'impronta del QR.
            final X509Certificate[][] chain = new X509Certificate[1][];
            TrustManager[] tm = {new X509TrustManager() {
                public void checkClientTrusted(X509Certificate[] c, String a) {}
                public void checkServerTrusted(X509Certificate[] c, String a) { chain[0] = c; }
                public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
            }};
            SSLContext sc = SSLContext.getInstance("TLS");
            sc.init(null, tm, new SecureRandom());
            HttpsURLConnection conn = (HttpsURLConnection) caUrl.openConnection();
            conn.setSSLSocketFactory(sc.getSocketFactory());
            conn.setHostnameVerifier((h, s) -> true);
            conn.setConnectTimeout(6000);
            conn.setReadTimeout(6000);
            if (conn.getResponseCode() != 200) {
                return "Il server non fornisce il certificato dell'evento (risposta " + conn.getResponseCode() + ").";
            }
            byte[] body = readAll(conn.getInputStream());
            Certificate[] served = conn.getServerCertificates();
            conn.disconnect();

            X509Certificate ca = (X509Certificate) CertificateFactory.getInstance("X.509")
                    .generateCertificate(new ByteArrayInputStream(body));
            if (!pin.equals(fingerprint(ca))) {
                return "Il certificato del server non corrisponde al QR: assicurati di essere sulla rete dell'evento.";
            }
            ca.checkValidity();
            // Il certificato presentato dal server deve essere firmato da questa CA.
            X509Certificate leaf = (X509Certificate) served[0];
            leaf.verify(ca.getPublicKey());
            prefs.edit().putString(k, Base64.encodeToString(ca.getEncoded(), Base64.NO_WRAP)).apply();
            return null;
        } catch (java.net.SocketTimeoutException | java.net.ConnectException e) {
            return "Server dell'evento non raggiungibile: controlla di essere sulla stessa rete Wi-Fi.";
        } catch (Exception e) {
            return "Verifica del certificato non riuscita: " + e.getMessage();
        }
    }

    /** Il certificato presentato al WebView è firmato dalla CA salvata per quell'indirizzo? */
    boolean trusts(String url, SslCertificate cert) {
        String saved = prefs.getString(key(url), null);
        if (saved == null || cert == null || android.os.Build.VERSION.SDK_INT < 29) return false;
        try {
            X509Certificate leaf = cert.getX509Certificate();
            if (leaf == null) return false;
            X509Certificate ca = (X509Certificate) CertificateFactory.getInstance("X.509")
                    .generateCertificate(new ByteArrayInputStream(Base64.decode(saved, Base64.NO_WRAP)));
            leaf.checkValidity();
            leaf.verify(ca.getPublicKey());
            return true;
        } catch (Exception e) {
            return false;
        }
    }

    static String fingerprint(X509Certificate c) throws Exception {
        byte[] h = MessageDigest.getInstance("SHA-256").digest(c.getEncoded());
        return Base64.encodeToString(h, Base64.URL_SAFE | Base64.NO_PADDING | Base64.NO_WRAP);
    }

    private static byte[] readAll(InputStream in) throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buf = new byte[4096];
        int n;
        while ((n = in.read(buf)) > 0) {
            out.write(buf, 0, n);
            if (out.size() > 64 * 1024) throw new Exception("certificato troppo grande");
        }
        in.close();
        return out.toByteArray();
    }
}
