package com.sourcegraph.find;

import com.intellij.ide.DataManager;
import com.intellij.openapi.Disposable;
import com.intellij.openapi.actionSystem.*;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.diagnostic.Logger;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.project.ProjectManager;
import com.intellij.openapi.project.ProjectManagerListener;
import com.intellij.openapi.ui.DialogBuilder;
import com.intellij.openapi.ui.DialogWrapper;
import com.intellij.openapi.ui.popup.ActiveIcon;
import com.intellij.openapi.ui.popup.ComponentPopupBuilder;
import com.intellij.openapi.ui.popup.JBPopup;
import com.intellij.openapi.ui.popup.JBPopupFactory;
import com.intellij.openapi.util.Disposer;
import com.intellij.openapi.wm.ex.WindowManagerEx;
import com.intellij.ui.popup.AbstractPopup;
import com.intellij.util.ui.UIUtil;
import com.sourcegraph.Icons;
import org.cef.browser.CefBrowser;
import org.cef.handler.CefKeyboardHandler;
import org.cef.misc.BoolRef;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import javax.swing.*;
import javax.swing.border.Border;
import java.awt.*;
import java.awt.event.*;

import static java.awt.event.InputEvent.ALT_DOWN_MASK;
import static java.awt.event.WindowEvent.WINDOW_GAINED_FOCUS;

public class FindService implements Disposable {
    private final Project project;
    private final FindPopupPanel mainPanel;
    private FindPopupDialog popup;
    private ActionListener myOkActionListener;
    private static final Logger logger = Logger.getInstance(FindService.class);

    public FindService(@NotNull Project project) {
        this.project = project;

        // Create main panel
        mainPanel = new FindPopupPanel(project);
    }

    synchronized public void showPopup() {
        if (popup == null || popup.isDisposed()) {
            createPopup();
        } else {
            popup.show();
        }

        // If the popup is already shown, hitting alt + a gain should behave the same as the native find in files
        // feature and focus the search field.
        if (mainPanel.getBrowser() != null) {
            mainPanel.getBrowser().focus();
        }
    }

    public void hidePopup() {
        popup.hide();
        hideMaterialUiOverlay();
    }

    @NotNull
    private void createPopup() {
        /*ComponentPopupBuilder builder = JBPopupFactory.getInstance().createComponentPopupBuilder(mainPanel, mainPanel)
            .setTitle("Sourcegraph")
            .setTitleIcon(new ActiveIcon(Icons.Logo))
            .setProject(project)
            .setModalContext(false)
            .setCancelOnClickOutside(true)
            .setRequestFocus(true)
            .setCancelKeyEnabled(false)
            .setResizable(true)
            .setMovable(true)
            .setLocateWithinScreenBounds(false)
            .setFocusable(true)
            .setCancelOnWindowDeactivation(false)
            .setCancelOnClickOutside(true)
            .setBelongsToGlobalPopupStack(true)
            .setMinSize(new Dimension(750, 420))
            .setNormalWindowLevel(true);
        */



        if (popup != null && popup.isVisible()) {
            return;
        }
        if (popup != null && !popup.isDisposed()) {
            popup.doCancelAction();
        }
        if (popup == null || popup.isDisposed()) {
            popup = new FindPopupDialog(project,mainPanel);

            // For some reason, adding a cancelCallback will prevent the cancel event to fire when using the escape key. To
            // work around this, we add a manual listener to both the global key handler (since the editor component seems
            // to work around the default swing event hands long) and the browser panel which seems to handle events in a
            // separate queue.
            registerGlobalKeyListeners();
            registerJBCefClientKeyListeners();

        }

    }

    private void registerGlobalKeyListeners() {
        KeyboardFocusManager.getCurrentKeyboardFocusManager()
            .addKeyEventDispatcher(e -> {
                if (e.getID() != KeyEvent.KEY_PRESSED || popup != null  && (popup.isDisposed() || !popup.isVisible() )) {
                    return false;
                }

                return handleKeyPress(false, e.getKeyCode(), e.getModifiersEx());
            });
    }

    private void registerJBCefClientKeyListeners() {
        if (mainPanel.getBrowser() == null) {
            logger.error("Browser panel is null");
            return;
        }

        mainPanel.getBrowser().getJBCefClient().addKeyboardHandler(new CefKeyboardHandler() {
            @Override
            public boolean onPreKeyEvent(CefBrowser browser, CefKeyEvent event, BoolRef is_keyboard_shortcut) {
                return false;
            }

            @Override
            public boolean onKeyEvent(CefBrowser browser, CefKeyEvent event) {
                return handleKeyPress(true, event.windows_key_code, event.modifiers);
            }
        }, mainPanel.getBrowser().getCefBrowser());
    }

    private boolean handleKeyPress(boolean isWebView, int keyCode, int modifiers) {
        if (keyCode == KeyEvent.VK_ESCAPE && modifiers == 0) {
            ApplicationManager.getApplication().invokeLater(this::hidePopup);
            return true;
        }


        if (!isWebView && keyCode == KeyEvent.VK_ENTER && (modifiers & ALT_DOWN_MASK) == ALT_DOWN_MASK) {
            if (mainPanel.getPreviewPanel() != null && mainPanel.getPreviewPanel().getPreviewContent() != null) {
                ApplicationManager.getApplication().invokeLater(() -> {
                    try {
                        mainPanel.getPreviewPanel().getPreviewContent().openInEditorOrBrowser();
                    } catch (Exception e) {
                        logger.error("Error opening file in editor", e);
                    }
                });
                return true;
            }
        }

        return false;
    }

    @Override
    public void dispose() {
        if(popup!=null){
            popup.getWindow().dispose();
        }
        mainPanel.dispose();
    }


    // We manually emit an action defined by the material UI theme to hide the overlay it opens whenever a popover is
    // created. This third-party plugin does not work with our approach of keeping the popover alive and thus, when the
    // Sourcegraph popover is closed, their custom overlay stays active.
    //
    //   - https://github.com/sourcegraph/sourcegraph/issues/36479
    //   - https://github.com/mallowigi/material-theme-issues/issues/179
    private void hideMaterialUiOverlay() {
        AnAction materialAction = ActionManager.getInstance().getAction("MTToggleOverlaysAction");
        if (materialAction != null) {
            try {
                DataContext dataContext = DataManager.getInstance().getDataContextFromFocusAsync().blockingGet(10);
                if (dataContext != null) {
                    materialAction.actionPerformed(
                        new AnActionEvent(
                            null,
                            dataContext,
                            ActionPlaces.UNKNOWN,
                            new Presentation(),
                            ActionManager.getInstance(),
                            0)
                    );
                }
            } catch (Exception ignored) {
            }
        }
    }
}
