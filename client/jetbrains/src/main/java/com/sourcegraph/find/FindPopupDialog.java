package com.sourcegraph.find;

import com.intellij.find.impl.FindPopupPanel;
import com.intellij.openapi.Disposable;
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
import com.intellij.util.Alarm;
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
import static java.awt.event.WindowEvent.*;

public class FindPopupDialog extends DialogWrapper {
    private JComponent myMainPanel;
    private Project myProject;
    private Canceller myMouseOutCanceller;
    private boolean myMouseEverEntered;


    public FindPopupDialog(@Nullable Project project, JComponent myMainPanel) {
        super(project, false);
        myProject = project;
        String appName = ApplicationNamesInfo.getInstance().getFullProductName();
        setTitle("Sourcegraph");
        setResizable(true);
        setAutoAdjustable(true);
        setModal(true);
        setCrossClosesWindow(false);
        setUndecorated(true);
        getWindow().setMinimumSize(new Dimension(750, 420));
        this.myMainPanel = myMainPanel;
        myMouseEverEntered = false;
        myMouseOutCanceller = new Canceller();
        Toolkit.getDefaultToolkit().addAWTEventListener(myMouseOutCanceller,
                MOUSE_EVENT_MASK | WINDOW_ACTIVATED | WINDOW_GAINED_FOCUS | MOUSE_MOTION_EVENT_MASK);
        init();
        show();

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

    public void hide() {
        getPeer().getWindow().setVisible(false);
        myMouseEverEntered = false;
    }


    //Copied over from com.intellij.ui.popup.AbstractPopup
    private class Canceller implements AWTEventListener {

        @Override
        public void eventDispatched(final AWTEvent event) {
            switch (event.getID()) {
                case WINDOW_GAINED_FOCUS:
                        Window dialogWindow = getPeer().getWindow();
                        Component focusOwner = IdeFocusManager.getInstance(myProject).getFocusOwner();
                        Window w = ComponentUtil.getWindow(focusOwner);
                        if(dialogWindow.isVisible()){
                            if(w != null && w.getOwner()!= null){
                                if( ((WindowEvent) event).getWindow() != w.getOwner() ) {
                                    hide();
                                }
                            }
                        }

                    break;
                case MOUSE_EXITED:
                    if (getPeer().getWindow().isVisible() && mouseWithinPopup(event)) {
                        myMouseEverEntered = true;
                    }
                    break;
                case MOUSE_PRESSED:
                    if (getPeer().getWindow().isVisible() && myMouseEverEntered)
                        if(!mouseWithinPopup(event)) {
                        hide();
                    }
                    break;
            }
        }
    }

    @Override
    public void show() {
        super.show();
    }

    private boolean mouseWithinPopup(@NotNull AWTEvent event) {
        final MouseEvent mouse = (MouseEvent)event;
        Rectangle bounds = getBoundsOnScreen(getWindow());
        return bounds != null && bounds.contains(mouse.getLocationOnScreen());
    }

    private static @Nullable Point getLocationOnScreen(@Nullable Component component) {
        return component == null || !component.isShowing() ? null : component.getLocationOnScreen();
    }

    private static @Nullable Rectangle getBoundsOnScreen(@Nullable Component component) {
        Point point = getLocationOnScreen(component);
        return point == null ? null : new Rectangle(point, component.getSize());
    }
}
