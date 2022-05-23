package com.sourcegraph.find;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.ui.Splitter;
import com.intellij.openapi.util.Disposer;
import com.intellij.ui.OnePixelSplitter;
import com.intellij.ui.PopupBorder;
import com.intellij.ui.components.JBPanel;
import com.intellij.ui.components.JBPanelWithEmptyText;
import com.intellij.ui.jcef.JBCefBrowser;
import com.intellij.util.ui.JBUI;
import com.sourcegraph.browser.JSToJavaBridge;
import com.sourcegraph.browser.JSToJavaBridgeRequestHandler;
import com.sourcegraph.config.ThemeUtil;

import java.awt.*;

/**
 * Inspired by <a href="https://sourcegraph.com/github.com/JetBrains/intellij-community/-/blob/platform/lang-impl/src/com/intellij/find/impl/FindPopupPanel.java">FindPopupPanel.java</a>
 */
public class FindPopupPanel extends JBPanel<FindPopupPanel> {

    public FindPopupPanel(Project project, JBCefBrowser browser) {
        super(new BorderLayout());

        setPreferredSize(JBUI.size(1200, 800));
        setBorder(PopupBorder.Factory.create(true, true));
        setFocusCycleRoot(true);

        // Create splitter
        Splitter splitter = new OnePixelSplitter(true, 0.5f, 0.1f, 0.9f);
        add(splitter, BorderLayout.CENTER);

        PreviewPanel previewPanel = new PreviewPanel(project);
        JBPanelWithEmptyText jcefPanel = new JBPanelWithEmptyText(new BorderLayout()).withEmptyText("Unfortunately, the browser is not available on your system. Try running the IDE with the default OpenJDK.");
        jcefPanel.add(browser.getComponent(), BorderLayout.CENTER);
        splitter.setFirstComponent(jcefPanel);
        splitter.setSecondComponent(previewPanel);

        String initJSCode = "window.initializeSourcegraph(" + (ThemeUtil.isDarkTheme() ? "true" : "false") + ");";
        JSToJavaBridgeRequestHandler requestHandler = new JSToJavaBridgeRequestHandler(previewPanel);
        JSToJavaBridge jsToJavaBridge = new JSToJavaBridge(browser, requestHandler, initJSCode);


        Disposer.register(browser, jsToJavaBridge);
    }

}
