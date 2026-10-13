package it.andreabattaglioli.charlie26;

import android.content.Intent;
import android.net.Uri;
import android.net.http.SslError;
import android.webkit.SslErrorHandler;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;

import com.getcapacitor.Bridge;
import com.getcapacitor.BridgeWebViewClient;

/**
 * WebView dell'app: accetta il certificato locale dell'evento solo se firmato
 * dalla CA verificata con il QR, e apre nel browser normale i link esterni
 * (es. il sito nei credits) invece di lasciare l'intercom.
 */
class PinnedWebViewClient extends BridgeWebViewClient {
    private final MainActivity activity;

    PinnedWebViewClient(Bridge bridge, MainActivity activity) {
        super(bridge);
        this.activity = activity;
    }

    @Override
    public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
        if (activity.trust.trusts(error.getUrl(), error.getCertificate())) {
            handler.proceed();
        } else {
            handler.cancel();
            activity.showStartError("Certificato dell'evento non riconosciuto: inquadra di nuovo il QR dalla console di regia.");
        }
    }

    @Override
    public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
        Uri url = request.getUrl();
        String scheme = url.getScheme();
        if ("https".equals(scheme) && url.getHost() != null && !"localhost".equals(url.getHost()) && !activity.isEventUrl(url.toString())) {
            // Link esterno: browser del telefono, l'intercom resta collegato.
            try {
                activity.startActivity(new Intent(Intent.ACTION_VIEW, url));
            } catch (Exception ignored) {
            }
            return true;
        }
        return super.shouldOverrideUrlLoading(view, request);
    }
}
