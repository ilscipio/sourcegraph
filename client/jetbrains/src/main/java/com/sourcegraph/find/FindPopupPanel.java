package com.sourcegraph.find;

import com.intellij.openapi.Disposable;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.ui.Splitter;
import com.intellij.openapi.util.IconLoader;
import com.intellij.ui.AnimatedIcon;
import com.intellij.ui.OnePixelSplitter;
import com.intellij.ui.PopupBorder;
import com.intellij.ui.components.JBLayeredPane;
import com.intellij.ui.components.JBPanel;
import com.intellij.ui.components.JBPanelWithEmptyText;
import com.intellij.ui.jcef.JBCefApp;
import com.intellij.util.ui.JBUI;
import com.sourcegraph.browser.JSToJavaBridgeRequestHandler;
import com.sourcegraph.browser.SourceGraphIcons;
import com.sourcegraph.browser.SourcegraphJBCefBrowser;
import com.sourcegraph.config.ThemeUtil;
import org.cef.browser.CefBrowser;
import org.cef.browser.CefFrame;
import org.cef.handler.CefLoadHandler;
import org.cef.network.CefRequest;
import org.jetbrains.annotations.Nullable;

import javax.swing.*;
import java.awt.*;

/**
 * Inspired by <a href="https://sourcegraph.com/github.com/JetBrains/intellij-community/-/blob/platform/lang-impl/src/com/intellij/find/impl/FindPopupPanel.java">FindPopupPanel.java</a>
 */
public class FindPopupPanel extends JBPanel<FindPopupPanel> implements Disposable {
    private final SourcegraphJBCefBrowser browser;

    public FindPopupPanel(Project project) {
        super(new BorderLayout());

        setPreferredSize(JBUI.size(1200, 800));
        setBorder(PopupBorder.Factory.create(true, true));
        setFocusCycleRoot(true);

        // Create splitter
        Splitter splitter = new OnePixelSplitter(true, 0.5f, 0.1f, 0.9f);
        add(splitter, BorderLayout.CENTER);

        JLayeredPane lpane = new JLayeredPane() {
            @Override
            public void doLayout() {
                final Component[] components = getComponents();
                final Rectangle r = getBounds();
                for (Component c : components) {
                    c.setBounds(0, 0, r.width, r.height);
                }
            }

            @Override
            public Dimension getPreferredSize() {
                return getBounds().getSize();
            }
        };


        PreviewPanel previewPanel = new PreviewPanel(project);

        JBPanelWithEmptyText jcefPanel = new JBPanelWithEmptyText(new BorderLayout()).withEmptyText("Unfortunately, the browser is not available on your system. Try running the IDE with the default OpenJDK.");
        lpane.add(jcefPanel, 100);

        // Create overlay with animation
        JBPanel overlay = new JBPanel().withBackground(ThemeUtil.gePanelBackgroundColor());
        ImageIcon imageIcon = new ImageIcon(IconLoader.toImage(SourceGraphIcons.DEFAULT));
        JLabel iconLabel = new JLabel();
        iconLabel.setIcon(imageIcon);
        imageIcon.setImageObserver(iconLabel);
        JLabel label = new JLabel("Loading...");
        overlay.add(iconLabel);
        overlay.add(label);

        browser = JBCefApp.isSupported() ? new SourcegraphJBCefBrowser(new JSToJavaBridgeRequestHandler(previewPanel),overlay) : null;
        if (browser != null) {
            jcefPanel.add(browser.getComponent());
            lpane.add(overlay, 0);
        }


        splitter.setFirstComponent(lpane);
        splitter.setSecondComponent(previewPanel);
    }

    @Nullable
    public SourcegraphJBCefBrowser getBrowser() {
        return browser;
    }

    @Override
    public void dispose() {
        if (browser != null) {
            browser.dispose();
        }
    }
}
