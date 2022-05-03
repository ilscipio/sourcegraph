package com.sourcegraph.ui;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.util.Disposer;
import com.intellij.ui.jcef.JBCefApp;
import com.intellij.ui.jcef.JBCefBrowser;
import com.intellij.ui.jcef.JBCefBrowserBase;
import com.intellij.util.ui.UIUtil;
import com.sourcegraph.scheme.SchemeHandlerFactory;
import org.cef.CefApp;
import org.cef.browser.CefBrowser;
import org.cef.browser.CefFrame;
import org.cef.handler.CefLoadHandler;
import org.cef.network.CefRequest;

import javax.swing.*;
import java.awt.*;

public class JCEFWindow {
    private JPanel panel;
    private Project project;
    private JBCefBrowserBase browser;
    private CefBrowser cefBrowser;

    public JCEFWindow(Project project) {
        this.project = project;
        panel = new JPanel(new BorderLayout());

        if (!JBCefApp.isSupported()) {
            JLabel warningLabel = new JLabel("Unfortunately, the browser is not available on your system. Try running the IDE with the default OpenJDK.");
            panel.add(warningLabel);
            return;
        }

        this.browser = new JBCefBrowser("http://sourcegraph/html/index.html")
                .createBuilder()
               .setOffScreenRendering(true)
                .setUrl("http://sourcegraph/html/index.html")
                .createBrowser() ;
        this.cefBrowser= browser.getCefBrowser();
        cefBrowser.setWindowVisibility(false);
        CefApp
            .getInstance()
            .registerSchemeHandlerFactory(
    "http",
    "sourcegraph",
                new SchemeHandlerFactory()
            );

        browser.getJBCefClient().addLoadHandler(new CefLoadHandler() {
            @Override
            public void onLoadingStateChange(CefBrowser cefBrowser, boolean isLoading, boolean canGoBack, boolean canGoForward) {
            }
            @Override
            public void onLoadStart(CefBrowser cefBrowser, CefFrame frame, CefRequest.TransitionType transitionType) {
            }
            @Override
            public void onLoadEnd(CefBrowser cefBrowser, CefFrame frame, int httpStatusCode) {

                cefBrowser.setWindowVisibility(true);
            }
            @Override
            public void onLoadError(CefBrowser cefBrowser, CefFrame frame, ErrorCode errorCode, String errorText, String failedUrl) {
            }
        }, cefBrowser);

        String backgroundColor = "#" + Integer.toHexString(UIUtil.getPanelBackground().getRGB()).substring(2);
        this.browser.setPageBackgroundColor(backgroundColor);
        panel.add(this.browser.getComponent(),BorderLayout.CENTER);
        Disposer.register(project, this.browser);
    }

    public JPanel getContent() {
        return panel;
    }

    public void focus() {
        this.cefBrowser.setFocus(true);
    }
}
