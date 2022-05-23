package com.sourcegraph.find;

import com.intellij.openapi.Disposable;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.ui.popup.JBPopup;
import com.intellij.openapi.ui.popup.JBPopupFactory;
import com.intellij.ui.jcef.JBCefApp;
import com.intellij.ui.jcef.JBCefBrowser;
import com.intellij.ui.jcef.JBCefBrowserBase;
import com.intellij.ui.jcef.JBCefClient;
import com.sourcegraph.browser.HttpSchemeHandlerFactory;
import com.sourcegraph.config.ThemeUtil;
import org.cef.CefApp;
public class SearchWindow implements Disposable {
    private final Project project;
    private final JBCefClient client;
    private FindPopupPanel mainPanel;
    private JBCefBrowser browser;
    private JBPopup popup;


    public SearchWindow(Project project) {
        this.project = project;
        this.client = JBCefApp.getInstance().createClient();
        this.browser = JBCefBrowser
                .createBuilder()
                .setClient(this.client)
                .setOffScreenRendering(false)
                .setUrl("http://sourcegraph/html/index.html")
                .build();

        if (this.browser != null) {
            this.browser.createImmediately();
            this.browser.getCefBrowser().getUIComponent().setFocusTraversalKeysEnabled(true);
            this.browser.getJBCefClient().setProperty(JBCefClient.Properties.JS_QUERY_POOL_SIZE, 100);
            this.browser.setPageBackgroundColor(ThemeUtil.getPanelBackgroundColorHexString());
            this.browser.setProperty(JBCefBrowserBase.Properties.NO_CONTEXT_MENU, Boolean.TRUE);

            CefApp.getInstance().registerSchemeHandlerFactory("http", "sourcegraph", new HttpSchemeHandlerFactory());
        }

        // Create main panel
        this.mainPanel = new FindPopupPanel(project, this.browser);
        this.mainPanel.paint(this.mainPanel.getGraphics());
    }

    synchronized public void showPopup() {
        if (!hasDocument()) {
            return;
        }
        if (this.popup != null ) {
            this.popup.dispose();
            this.popup = null;
        }
        this.popup = JBPopupFactory.getInstance().createComponentPopupBuilder(this.mainPanel, null)
                .setTitle("Find on Sourcegraph")
                .setCancelOnClickOutside(true)
                .setResizable(true)
                .setModalContext(false)
                .setRequestFocus(true)
                .setFocusable(true)
                .setMovable(true)
                .setBelongsToGlobalPopupStack(true)
                .setCancelOnOtherWindowOpen(true)
                .setCancelKeyEnabled(true)
                .setNormalWindowLevel(true)
                .createPopup();
        this.popup.showCenteredInCurrentWindow(this.project);
        this.browser.getCefBrowser().getUIComponent().requestFocus();
    }

    private boolean hasDocument() {
        return this.browser.getCefBrowser().hasDocument();
    }


    @Override
    public void dispose() {
        if (popup != null) {
            popup.dispose();
        }
        if (browser != null) {
            browser.dispose();
        }
    }
}
