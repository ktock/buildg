(require 'dap-mode)
(require 'dap-utils)

(dap-register-debug-provider "dockerfile" 'dap-dockerfile--populate-default-args)

(defun dap-dockerfile--populate-default-args (conf)
  "Populate CONF with the default arguments."
  (-> conf
    (dap--put-if-absent :program buffer-file-name)
    (dap--put-if-absent :dap-server-path (list "buildg" "dap" "serve"))))

(dap-register-debug-template "Dockerfile Debug Configuration"
                             (list :type "dockerfile"
                                   :request "launch"
                                   :stopOnEntry t
                                   :name "Debug Dockerfile"))
