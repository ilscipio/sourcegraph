package com.sourcegraph.browser;

import com.intellij.openapi.util.Disposer;
import com.intellij.ui.components.JBPanel;
import com.intellij.ui.jcef.JBCefBrowser;
import com.intellij.ui.jcef.JBCefBrowserBase;
import com.sourcegraph.config.ThemeUtil;
import org.cef.CefApp;
import org.cef.browser.CefBrowser;
import org.cef.browser.CefFrame;
import org.cef.handler.CefLifeSpanHandler;
import org.cef.handler.CefLoadHandler;
import org.cef.network.CefRequest;
import org.jetbrains.annotations.NotNull;

import java.util.Timer;
import java.util.TimerTask;

public class SourcegraphJBCefBrowser extends JBCefBrowser {
    private final JSToJavaBridge jsToJavaBridge;


    public SourcegraphJBCefBrowser(JSToJavaBridgeRequestHandler requestHandler, JBPanel overlay) {
        super("http://sourcegraph/html/index.html");
        // Create and set up JCEF browser
        CefApp.getInstance().registerSchemeHandlerFactory("http", "sourcegraph", new HttpSchemeHandlerFactory());
        this.setPageBackgroundColor(ThemeUtil.getPanelBackgroundColorHexString());
        this.setProperty(JBCefBrowserBase.Properties.NO_CONTEXT_MENU, Boolean.TRUE);


        // Create bridge, set up handlers, then run init function
        String initJSCode = "window.initializeSourcegraph(" + (ThemeUtil.isDarkTheme() ? "true" : "false") + ");";
        jsToJavaBridge = new JSToJavaBridge(this, requestHandler, initJSCode);
        getJBCefClient().addLoadHandler(new CefLoadHandler() {
            @Override
            public void onLoadingStateChange(CefBrowser cefBrowser, boolean b, boolean b1, boolean b2) {
            }

            @Override
            public void onLoadStart(CefBrowser cefBrowser, CefFrame cefFrame, CefRequest.TransitionType transitionType) {
            }

            @Override
            public void onLoadEnd(CefBrowser cefBrowser, CefFrame cefFrame, int i) {
                overlay.setVisible(false);
                cefBrowser.setFocus(true);
            }
            @Override
            public void onLoadError(CefBrowser cefBrowser, CefFrame cefFrame, ErrorCode errorCode, String s, String s1) {}
        },getCefBrowser());


        Disposer.register(this, jsToJavaBridge);
    }

    public JSToJavaBridge getJsToJavaBridge() {
        return jsToJavaBridge;
    }

    public void focus() {
        this.getCefBrowser().setFocus(true);
    }
}
