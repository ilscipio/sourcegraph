package com.sourcegraph.find;

import com.intellij.find.impl.FindPopupPanel;
import com.intellij.openapi.actionSystem.AnActionEvent;
import com.intellij.openapi.actionSystem.CustomShortcutSet;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.application.ApplicationNamesInfo;
import com.intellij.openapi.application.ModalityState;
import com.intellij.openapi.project.DumbAwareAction;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.ui.DialogWrapper;
import com.intellij.openapi.ui.popup.ActiveIcon;
import com.intellij.openapi.ui.popup.JBPopup;
import com.intellij.openapi.ui.popup.JBPopupFactory;
import com.intellij.openapi.util.Disposer;
import com.intellij.openapi.wm.IdeFocusManager;
import com.intellij.openapi.wm.IdeFrame;
import com.intellij.openapi.wm.ex.WindowManagerEx;
import com.intellij.ui.ComponentUtil;
import com.intellij.ui.ExperimentalUI;
import com.intellij.ui.TitlePanel;
import com.intellij.ui.WindowMoveListener;
import com.intellij.ui.popup.AbstractPopup;
import com.intellij.util.containers.ContainerUtil;
import com.intellij.util.ui.ChildFocusWatcher;
import com.intellij.util.ui.JBUI;
import com.intellij.util.ui.UIUtil;
import com.sourcegraph.Icons;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import javax.swing.*;
import javax.swing.border.Border;
import java.awt.*;
import java.awt.event.*;
import java.util.Arrays;
import java.util.List;

import static java.awt.AWTEvent.MOUSE_EVENT_MASK;
import static java.awt.AWTEvent.MOUSE_MOTION_EVENT_MASK;
import static java.awt.event.MouseEvent.*;
import static java.awt.event.WindowEvent.WINDOW_ACTIVATED;
import static java.awt.event.WindowEvent.WINDOW_GAINED_FOCUS;

public class FindPopupDialog extends DialogWrapper {
    private JComponent myMainPanel;
    private Project myProject;
    private Canceller myMouseOutCanceller;

    public FindPopupDialog(@Nullable Project project, JComponent myMainPanel) {
        super(project, false);
        myProject = project;
        String appName = ApplicationNamesInfo.getInstance().getFullProductName();
        setTitle("Sourcegraph");
        setResizable(true);
        setModal(true);
        setCrossClosesWindow(false);
        setUndecorated(true);
        getWindow().setMinimumSize(new Dimension(750, 420));
        this.myMainPanel = myMainPanel;
        myMouseOutCanceller = new Canceller();
        Toolkit.getDefaultToolkit().addAWTEventListener(myMouseOutCanceller,
                MOUSE_EVENT_MASK | WINDOW_ACTIVATED | WINDOW_GAINED_FOCUS | MOUSE_MOTION_EVENT_MASK);


        init();

       // registerOutsideClickListener();
    }

    @Override
    protected @Nullable JComponent createCenterPanel() {
        JLabel icon = new JLabel(new ActiveIcon(Icons.Logo));
        TitlePanel titlePanel = new TitlePanel(new ActiveIcon(Icons.Logo).getRegular(), new ActiveIcon(Icons.Logo).getInactive());
        titlePanel.setText(getTitle());
        titlePanel.setPopupTitle(ExperimentalUI.isNewUI());
        icon.setVerticalAlignment(SwingConstants.TOP);

        //We have to reimplement the move listener
        WindowMoveListener windowListener = new WindowMoveListener(this.getWindow());
        titlePanel.addMouseListener(windowListener);
        titlePanel.addMouseMotionListener(windowListener);
        getWindow().addMouseListener(windowListener);
        getWindow().addMouseMotionListener(windowListener);

        //Adding the center panel
        return JBUI.Panels.simplePanel()
                .addToTop(titlePanel)
                .addToCenter(myMainPanel);
    }

    @Override
    protected JComponent createSouthPanel() {

        return null;
    }


    /**
     * Removes the 8px border */
    @Override
    protected @Nullable Border createContentPaneBorder() {
        return null;
    }

    private void hide() {
        getWindow().setVisible(false);
    }

    private void registerOutsideClickListener() {
        Window projectParentWindow = getParentWindow(null);

        Toolkit.getDefaultToolkit().addAWTEventListener(event -> {
            if (event instanceof WindowEvent) {
                WindowEvent windowEvent = (WindowEvent) event;

                // We only care for focus events
                if (windowEvent.getID() != WINDOW_GAINED_FOCUS) {
                    return;
                }

                // Detect if we're focusing the Sourcegraph popup
                Window sourcegraphPopupWindow = getWindow();

                if (windowEvent.getWindow().equals(sourcegraphPopupWindow)) {
                    return;
                }

                // Detect if the newly focused window is a parent of the project root window
                Window currentProjectParentWindow = getParentWindow(windowEvent.getComponent());
                if (currentProjectParentWindow.equals(projectParentWindow)) {
                    getWindow().setVisible(false);
                }
            }
        }, AWTEvent.WINDOW_EVENT_MASK);
    }

    // https://sourcegraph.com/github.com/JetBrains/intellij-community@27fee7320a01c58309a742341dd61deae57c9005/-/blob/platform/platform-impl/src/com/intellij/ui/popup/AbstractPopup.java?L475-493
    private Window getParentWindow(Component component) {
        Window window = null;
        Component parent = UIUtil.findUltimateParent(component == null ? WindowManagerEx.getInstanceEx().getFocusedComponent(myProject) : component);
        if (parent instanceof Window) {
            window = (Window) parent;
        }
        if (window == null) {
            window = KeyboardFocusManager.getCurrentKeyboardFocusManager().getFocusedWindow();
        }
        return window;
    }


    private class Canceller implements AWTEventListener {
        private boolean myEverEntered;

        @Override
        public void eventDispatched(final AWTEvent event) {
            switch (event.getID()) {
                case WINDOW_ACTIVATED:
                case WINDOW_GAINED_FOCUS:
                    if (this != null && isCancelNeeded((WindowEvent)event, getWindow())) {
                        hide();
                    }
                    break;
                case MOUSE_ENTERED:
                    if (withinPopup(event)) {
                        myEverEntered = true;
                    }
                    break;
                case MOUSE_MOVED:
                case MOUSE_PRESSED:
                    if ( myEverEntered && !withinPopup(event)) {
                        hide();
                    }
                    break;
            }
        }

        private boolean withinPopup(@NotNull AWTEvent event) {
            final MouseEvent mouse = (MouseEvent)event;
            Rectangle bounds = getBoundsOnScreen(getContentPanel());
            return bounds != null && bounds.contains(mouse.getLocationOnScreen());
        }
    }

    private static @Nullable Point getLocationOnScreen(@Nullable Component component) {
        return component == null || !component.isShowing() ? null : component.getLocationOnScreen();
    }

    private static @Nullable Rectangle getBoundsOnScreen(@Nullable Component component) {
        Point point = getLocationOnScreen(component);
        return point == null ? null : new Rectangle(point, component.getSize());
    }

    private static boolean isCancelNeeded(@NotNull WindowEvent event, @Nullable Window popup) {
        Window window = event.getWindow(); // the activated or focused window
        while (window != null) {
            if (popup == window) return false; // do not close a popup, which child is activated or focused
            window = window.getOwner(); // consider a window owner as activated or focused
        }
        return true;
    }
}
