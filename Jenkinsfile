// ============================================
// CityList API - Jenkins Pipeline
// Multi-architecture builds (amd64, arm64)
// ============================================

pipeline {
    agent none

    environment {
        PROJECTNAME = 'citylist'
        PROJECTORG = 'apimgr'
        REGISTRY = 'ghcr.io'
        IMAGE_NAME = "${REGISTRY}/apimgr/citylist"

        VERSION = sh(script: 'cat release.txt 2>/dev/null || echo "0.0.1"', returnStdout: true).trim()
        COMMIT = sh(script: 'git rev-parse --short HEAD 2>/dev/null || echo "unknown"', returnStdout: true).trim()
        BUILD_DATE = sh(script: 'date -u +%Y-%m-%dT%H:%M:%SZ', returnStdout: true).trim()
    }

    stages {
        stage('Build') {
            parallel {
                stage('Build AMD64') {
                    agent {
                        label 'amd64'
                    }
                    steps {
                        echo '🏗️  Building for AMD64...'
                        sh '''
                            make build
                            ls -lh binaries/
                        '''
                    }
                }

                stage('Build ARM64') {
                    agent {
                        label 'arm64'
                    }
                    steps {
                        echo '🏗️  Building for ARM64...'
                        sh '''
                            make build
                            ls -lh binaries/
                        '''
                    }
                }
            }
        }

        stage('Test') {
            parallel {
                stage('Test AMD64') {
                    agent {
                        label 'amd64'
                    }
                    steps {
                        echo '🧪 Running tests on AMD64...'
                        sh 'make test'
                    }
                    post {
                        always {
                            junit '**/test-results/*.xml'
                            publishCoverage adapters: [coberturaAdapter('coverage.out')]
                        }
                    }
                }

                stage('Test ARM64') {
                    agent {
                        label 'arm64'
                    }
                    steps {
                        echo '🧪 Running tests on ARM64...'
                        sh 'make test'
                    }
                    post {
                        always {
                            junit '**/test-results/*.xml'
                            publishCoverage adapters: [coberturaAdapter('coverage.out')]
                        }
                    }
                }
            }
        }

        stage('Build Docker Images') {
            parallel {
                stage('Build Docker AMD64') {
                    agent {
                        label 'amd64'
                    }
                    steps {
                        echo '🐳 Building Docker image for AMD64...'
                        script {
                            sh """
                                docker build \
                                    --platform linux/amd64 \
                                    --build-arg VERSION=${VERSION} \
                                    --build-arg COMMIT=${COMMIT} \
                                    --build-arg BUILD_DATE=${BUILD_DATE} \
                                    -t ${IMAGE_NAME}:${VERSION}-amd64 \
                                    -t ${IMAGE_NAME}:latest-amd64 \
                                    .
                            """
                        }
                    }
                }

                stage('Build Docker ARM64') {
                    agent {
                        label 'arm64'
                    }
                    steps {
                        echo '🐳 Building Docker image for ARM64...'
                        script {
                            sh """
                                docker build \
                                    --platform linux/arm64 \
                                    --build-arg VERSION=${VERSION} \
                                    --build-arg COMMIT=${COMMIT} \
                                    --build-arg BUILD_DATE=${BUILD_DATE} \
                                    -t ${IMAGE_NAME}:${VERSION}-arm64 \
                                    -t ${IMAGE_NAME}:latest-arm64 \
                                    .
                            """
                        }
                    }
                }
            }
        }

        stage('Push Docker Images') {
            agent {
                label 'amd64'
            }
            when {
                anyOf {
                    branch 'main'
                    branch 'master'
                }
            }
            steps {
                echo '📤 Pushing Docker images to registry...'
                script {
                    withCredentials([usernamePassword(
                        credentialsId: 'github-registry',
                        usernameVariable: 'REGISTRY_USER',
                        passwordVariable: 'REGISTRY_TOKEN'
                    )]) {
                        sh """
                            echo \$REGISTRY_TOKEN | docker login ${REGISTRY} -u \$REGISTRY_USER --password-stdin

                            # Push individual arch images
                            docker push ${IMAGE_NAME}:${VERSION}-amd64
                            docker push ${IMAGE_NAME}:${VERSION}-arm64
                            docker push ${IMAGE_NAME}:latest-amd64
                            docker push ${IMAGE_NAME}:latest-arm64

                            # Create multi-arch manifests
                            docker manifest create ${IMAGE_NAME}:${VERSION} \
                                ${IMAGE_NAME}:${VERSION}-amd64 \
                                ${IMAGE_NAME}:${VERSION}-arm64

                            docker manifest create ${IMAGE_NAME}:latest \
                                ${IMAGE_NAME}:latest-amd64 \
                                ${IMAGE_NAME}:latest-arm64

                            # Push manifests
                            docker manifest push ${IMAGE_NAME}:${VERSION}
                            docker manifest push ${IMAGE_NAME}:latest

                            # Logout
                            docker logout ${REGISTRY}
                        """
                    }
                }
            }
        }

        stage('GitHub Release') {
            agent {
                label 'amd64'
            }
            when {
                tag pattern: "v\\d+\\.\\d+\\.\\d+", comparator: "REGEXP"
            }
            steps {
                echo '📦 Creating GitHub release...'
                script {
                    def tagName = env.TAG_NAME
                    def version = tagName.replaceFirst(/^v/, '')

                    withCredentials([string(credentialsId: 'github-token', variable: 'GH_TOKEN')]) {
                        sh """
                            export GH_TOKEN=\$GH_TOKEN
                            make release VERSION=${version}
                        """
                    }
                }
            }
        }
    }

    post {
        success {
            echo '✅ Pipeline completed successfully!'
        }
        failure {
            echo '❌ Pipeline failed!'
        }
        cleanup {
            echo '🧹 Cleaning up workspace...'
            cleanWs()
        }
    }
}
